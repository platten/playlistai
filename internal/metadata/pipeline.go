package metadata

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
)

// ImportWorkers bounds both scheduler overhead and in-flight decoded records.
func ImportWorkers(requested int) (int, error) {
	if requested < 0 || requested > 32 {
		return 0, errors.New("import workers must be between 0 (automatic) and 32")
	}
	if requested == 0 {
		return min(8, runtime.GOMAXPROCS(0)), nil
	}
	return requested, nil
}

type entityResult struct {
	e   preparedEntity
	err error
}

type entityJob struct {
	raw    []byte
	kind   string
	result chan entityResult
}

// processEntities overlaps gzip/framing, XML decoding + identity matching, and
// SQLite writes. Per-job reply channels keep writes in source order even when
// workers finish out of order. The bounded ticket queue prevents a slow early
// record from accumulating an unbounded reorder buffer. Only consume mutates DB,
// tag vocabulary, master dependencies, manifest counts or progress callbacks.
func processEntities(ctx context.Context, r io.Reader, workers int, lookup map[string]string, consume func(string, preparedEntity) error) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	jobs := make(chan entityJob)
	ordered := make(chan entityJob, workers*2)
	finished := make(chan error, 1)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				var e entity
				err := xml.NewDecoder(contextReader{ctx, bytes.NewReader(job.raw)}).Decode(&e)
				var prepared preparedEntity
				if err == nil && ctx.Err() == nil {
					prepared = prepareEntity(e, lookup)
				}
				// Buffered reply never waits for a canceled writer.
				job.result <- entityResult{prepared, err}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(jobs)
		defer close(ordered)
		finished <- frameEntities(contextReader{ctx, r}, func(kind string, raw []byte) error {
			job := entityJob{raw: raw, kind: kind, result: make(chan entityResult, 1)}
			// Do not retain raw XML in the ordered queue after decoding.
			select {
			case ordered <- entityJob{kind: kind, result: job.result}:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case jobs <- job:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job, ok := <-ordered:
			if !ok {
				return <-finished
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case result := <-job.result:
				if result.err != nil {
					return fmt.Errorf("decode Discogs %s: %w", job.kind, result.err)
				}
				if err := consume(job.kind, result.e); err != nil {
					return err
				}
			}
		}
	}
}

const maxEntityBytes = 16 << 20

// frameEntities is only a lexical splitter, not a replacement XML parser. It
// recognizes markup boundaries (including quotes, CDATA, comments and PIs), so
// workers can perform the expensive encoding/xml validation and struct decoding
// independently. Discogs has no DTD or inherited namespaces; reject those rather
// than silently changing entity expansion or namespace semantics. Each record is
// bounded to 16 MiB; malformed/oversized dumps fail without publication.
func frameEntities(r io.Reader, emit func(string, []byte) error) error {
	b := bufio.NewReaderSize(r, 128<<10)
	var record []byte
	var markup []byte
	root, kind := "", ""
	depth := 0
	closed := false
	for {
		part, err := b.ReadSlice('<')
		text := part
		if err == nil {
			text = part[:len(part)-1]
		}
		if depth > 0 {
			if len(record)+len(text) > maxEntityBytes {
				return errors.New("discogs entity exceeds 16 MiB limit")
			}
			record = append(record, text...)
		} else if len(bytes.TrimSpace(text)) != 0 {
			return errors.New("unexpected text outside Discogs entity")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF {
			if !closed || depth != 0 {
				return io.ErrUnexpectedEOF
			}
			return nil
		}
		if err != nil {
			return err
		}
		markup, err = readMarkup(b, markup[:0])
		if err != nil {
			return err
		}
		if depth > 0 {
			if len(record)+len(markup) > maxEntityBytes {
				return errors.New("discogs entity exceeds 16 MiB limit")
			}
			record = append(record, markup...)
		}
		if bytes.HasPrefix(markup, []byte("<!")) || bytes.HasPrefix(markup, []byte("<?")) {
			if depth == 0 {
				// Validate comments/PIs, and reject CDATA outside the document root.
				token, err := xml.NewDecoder(bytes.NewReader(markup)).Token()
				if err != nil {
					return err
				}
				if _, ok := token.(xml.CharData); ok {
					return errors.New("CDATA outside Discogs entity")
				}
			}
			continue
		}
		ending := markup[1] == '/'
		selfClosing := len(markup) > 2 && markup[len(markup)-2] == '/'
		if depth == 0 {
			if closed {
				return errors.New("multiple XML roots")
			}
			if ending {
				token, err := xml.NewDecoder(bytes.NewReader(markup)).RawToken()
				end, ok := token.(xml.EndElement)
				if err != nil || !ok || end.Name.Space != "" || end.Name.Local != root {
					return errors.New("invalid Discogs root closing tag")
				}
				closed = true
				continue
			}
			if root == "" {
				token, err := xml.NewDecoder(bytes.NewReader(markup)).Token()
				if err != nil {
					return err
				}
				start, ok := token.(xml.StartElement)
				if !ok || start.Name.Space != "" {
					return errors.New("expected Discogs XML element")
				}
				root = start.Name.Local
				if (root != "releases" && root != "masters") || len(start.Attr) != 0 {
					return errors.New("expected unnamespaced Discogs releases or masters root")
				}
				closed = selfClosing
				continue
			}
			kind = strings.TrimSuffix(root, "s")
			name := markup[1:]
			if end := bytes.IndexAny(name, " \t\r\n/>"); end >= 0 {
				name = name[:end]
			}
			if string(name) != kind {
				return fmt.Errorf("unexpected %s in Discogs %s", name, root)
			}
			record = append([]byte(nil), markup...)
		}
		if ending {
			depth--
		} else if !selfClosing {
			depth++
		}
		if depth == 0 {
			if err := emit(kind, record); err != nil {
				return err
			}
			record = nil // ownership passed to worker
		}
	}
}

func readMarkup(b *bufio.Reader, part []byte) ([]byte, error) {
	part = append(part, '<')
	quote := byte(0)
	terminator := ""
	for len(part) <= maxEntityBytes {
		c, err := b.ReadByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		part = append(part, c)
		if len(part) == 2 && c == '?' {
			terminator = "?>"
		}
		if len(part) >= 2 && part[1] == '!' && terminator == "" {
			switch {
			case string(part) == "<!--":
				terminator = "-->"
			case string(part) == "<![CDATA[":
				terminator = "]]>"
			case strings.HasPrefix("<!--", string(part)), strings.HasPrefix("<![CDATA[", string(part)): //nolint:gocritic // Accumulated bytes must be a prefix of either full opening delimiter.
				continue
			default:
				return nil, errors.New("DTD/declarations are not supported in Discogs dumps")
			}
		}
		if terminator != "" {
			if bytes.HasSuffix(part, []byte(terminator)) {
				return part, nil
			}
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
		} else if c == '\'' || c == '"' {
			quote = c
		} else if c == '>' {
			return part, nil
		}
	}
	return nil, errors.New("discogs markup exceeds 16 MiB limit")
}

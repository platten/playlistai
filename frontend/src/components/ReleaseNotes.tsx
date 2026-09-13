import { memo } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Browser } from "@wailsio/runtime";
import "./ReleaseNotes.css";

// Release content is remote text, never executable HTML or a source of
// automatic image requests. Only explicit web links can leave the app.
function webURL(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : "";
  } catch { return ""; }
}

export const ReleaseNotes = memo(function ReleaseNotes({ notes, onLinkError }: {
  notes: string;
  onLinkError: (message: string) => void;
}) {
  return <div className="release-notes">
    <Markdown remarkPlugins={[remarkGfm]} skipHtml urlTransform={webURL} components={{
      // Fit document headings beneath the dialog and What's new headings.
      h1: ({ children }) => <h4 className="release-notes-title">{children}</h4>,
      h2: ({ children }) => <h4>{children}</h4>,
      h3: ({ children }) => <h5>{children}</h5>,
      h4: ({ children }) => <h6>{children}</h6>,
      h5: ({ children }) => <h6>{children}</h6>,
      h6: ({ children }) => <h6>{children}</h6>,
      a: ({ href, children }) => href ? <a href={href} target="_blank" rel="noopener noreferrer"
        onClick={(event) => {
          event.preventDefault();
          void Browser.OpenURL(href).catch((error) => onLinkError(`Could not open release-note link: ${String(error)}`));
        }}>{children}</a> : <span>{children}</span>,
      img: ({ alt }) => <span>{alt}</span>,
      table: ({ children }) => <div className="release-notes-table" tabIndex={0} role="region" aria-label="Release notes table"><table>{children}</table></div>,
    }}>{notes}</Markdown>
  </div>;
});

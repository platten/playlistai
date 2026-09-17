package libraryindex

import "time"

// ProcessingIssue is an event suitable for the private state issue log. Path is
// relative to RootAlias; Detail may contain native or operating-system paths.
type ProcessingIssue struct {
	Time      time.Time `json:"time"`
	Stage     string    `json:"stage"`
	RootAlias string    `json:"rootAlias,omitempty"`
	Path      string    `json:"path,omitempty"`
	Code      string    `json:"code"`
	Detail    string    `json:"detail"`
	Retryable bool      `json:"retryable"`
}

func NewProcessingIssue(stage, rootAlias, path, code string, err error, retryable bool) ProcessingIssue {
	detail := ""
	if err != nil {
		detail = boundedAnalysisDetail(err.Error())
	}
	return ProcessingIssue{Time: time.Now().UTC(), Stage: stage, RootAlias: rootAlias, Path: path, Code: code, Detail: detail, Retryable: retryable}
}

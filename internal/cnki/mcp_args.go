// The public tool arguments: the whole tool contract in one file.

package cnki

type LoginArgs struct {
	RemoteAccessURL string `json:"remote_access_url,omitempty" jsonschema:"institutional remote-access entry (HTTPS)"`
	Force           bool   `json:"force,omitempty" jsonschema:"open the window even if logged in"`
}

type StatusArgs struct {
	OperationID string `json:"operation_id,omitempty"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"1-55, default 50"`
	Cancel      bool   `json:"cancel,omitempty"`
	UserDone    bool   `json:"user_done,omitempty" jsonschema:"manual step finished (institutional remote login)"`
	Show        bool   `json:"show,omitempty" jsonschema:"bring the verification window to the front"`
	Recover     bool   `json:"recover,omitempty" jsonschema:"restart the browser"`
	CheckLogin  bool   `json:"check_login,omitempty" jsonschema:"check the login on a background page"`
	Details     bool   `json:"details,omitempty" jsonschema:"local diagnostics"`
}

// SearchOptions are the bounds of one research search.
type SearchOptions struct {
	Metadata        string `json:"metadata,omitempty" jsonschema:"default ranked: detail pages of the best candidates only"`
	CandidateTarget int    `json:"candidate_target,omitempty" jsonschema:"default 300"`
	MaxPages        int    `json:"max_pages,omitempty" jsonschema:"pages per channel, default 5"`
	MaxRequests     int    `json:"max_requests,omitempty" jsonschema:"request budget, default 600"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty" jsonschema:"active-time budget, default 900"`
	OnVerification  string `json:"on_verification,omitempty" jsonschema:"default ask: window for the user"`
}

type SearchArgs struct {
	Query         string         `json:"query,omitempty" jsonschema:"research question or keywords"`
	ConceptGroups [][]string     `json:"concept_groups,omitempty" jsonschema:"AND of synonym OR-groups, e.g. [[\"违约金\",\"违约赔偿\"],[\"调整\"]]"`
	Expression    *Expression    `json:"expression,omitempty" jsonschema:"native field search instead of query"`
	Mode          string         `json:"mode,omitempty" jsonschema:"default balanced; precise skips the feedback round"`
	YearFrom      int            `json:"year_from,omitempty"`
	YearTo        int            `json:"year_to,omitempty"`
	Sort          string         `json:"sort,omitempty" jsonschema:"default relevance"`
	Limit         int            `json:"limit,omitempty" jsonschema:"rows shown, default 10, max 50"`
	View          string         `json:"view,omitempty"`
	Refresh       bool           `json:"refresh,omitempty" jsonschema:"ignore the 10-minute cache"`
	Options       *SearchOptions `json:"options,omitempty"`
}

type ResultsArgs struct {
	SearchID           string `json:"search_id"`
	Scope              string `json:"scope,omitempty" jsonschema:"default selected"`
	Offset             int    `json:"offset,omitempty"`
	Limit              int    `json:"limit,omitempty" jsonschema:"default 10, max 100"`
	View               string `json:"view,omitempty"`
	Continue           bool   `json:"continue,omitempty" jsonschema:"resume a partial search"`
	AdditionalRequests int    `json:"additional_requests,omitempty" jsonschema:"default 200"`
}

type MetadataArgs struct {
	RecordRefs []string `json:"record_refs,omitempty"`
	Titles     []string `json:"titles,omitempty" jsonschema:"exact full titles"`
	SearchID   string   `json:"search_id,omitempty"`
	Scope      string   `json:"scope,omitempty" jsonschema:"default selected"`
	Offset     int      `json:"offset,omitempty"`
	Limit      int      `json:"limit,omitempty" jsonschema:"rows of search_id, default 10, max 100"`
	Fields     []string `json:"fields,omitempty" jsonschema:"keys to return"`
	Quality    bool     `json:"quality,omitempty" jsonschema:"add journal indexing and impact factors"`
	Refresh    bool     `json:"refresh,omitempty" jsonschema:"read detail pages again"`
	LocalOnly  bool     `json:"local_only,omitempty" jsonschema:"no network: stored records only"`
}

type ReadArgs struct {
	RecordRef      string `json:"record_ref,omitempty"`
	Title          string `json:"title,omitempty" jsonschema:"exact full title instead of record_ref"`
	SnapshotID     string `json:"snapshot_id,omitempty" jsonschema:"continue saved text without network"`
	Section        string `json:"section,omitempty" jsonschema:"only this section (a title from sections)"`
	Offset         int    `json:"offset,omitempty"`
	MaxChars       int    `json:"max_characters,omitempty" jsonschema:"default 8000, max 30000"`
	Refresh        bool   `json:"refresh,omitempty" jsonschema:"reopen the reader instead of the saved text"`
	OnVerification string `json:"on_verification,omitempty"`
}

type DownloadArgs struct {
	RecordRef      string `json:"record_ref,omitempty"`
	Title          string `json:"title,omitempty" jsonschema:"exact full title instead of record_ref"`
	Format         string `json:"format,omitempty" jsonschema:"default auto (PDF first)"`
	Filename       string `json:"filename,omitempty" jsonschema:"a basename; never overwrites"`
	Subdirectory   string `json:"subdirectory,omitempty" jsonschema:"folder under data/downloads/"`
	WaitSeconds    int    `json:"wait_seconds,omitempty" jsonschema:"default 120"`
	Retry          bool   `json:"retry,omitempty" jsonschema:"click again after an unknown outcome"`
	OnVerification string `json:"on_verification,omitempty"`
}

type ExportArgs struct {
	RecordRefs []string `json:"record_refs,omitempty"`
	Titles     []string `json:"titles,omitempty" jsonschema:"exact full titles"`
	SearchID   string   `json:"search_id,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	Format     string   `json:"format,omitempty" jsonschema:"default gbt7714"`
	Template   string   `json:"template,omitempty" jsonschema:"for custom: {authors}. {title}[J]. {journal}, {year}"`
	Filename   string   `json:"filename,omitempty" jsonschema:"save under data/exports/"`
}

type LinkArgs struct {
	Text          string `json:"text" jsonschema:"text with 《full paper titles》"`
	LookupMissing bool   `json:"lookup_missing,omitempty" jsonschema:"locate titles not stored"`
}

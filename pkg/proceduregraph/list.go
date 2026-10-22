package proceduregraph

const ProcedureListSchemaVersion = "threadforge.procedure-list.v1"

// ProcedureListResponse is a bounded, metadata-only index of approved
// procedures. Instructions are retrieved separately through a freshness-check
// endpoint, so listing cannot accidentally make stale guidance model-visible.
type ProcedureListResponse struct {
	SchemaVersion string              `json:"schema_version"`
	TotalCount    int                 `json:"total_count"`
	Count         int                 `json:"count"`
	Offset        int                 `json:"offset"`
	HasMore       bool                `json:"has_more"`
	NextOffset    int                 `json:"next_offset"`
	Procedures    []ProcedureListItem `json:"procedures"`
}

// ProcedureListItem contains identity and source-freshness metadata only.
// InstructionsIncluded is always false; callers must use procedure_get or
// procedure_guide to request guidance under their freshness contract.
type ProcedureListItem struct {
	ID                   string   `json:"id"`
	Revision             int      `json:"revision"`
	Title                string   `json:"title"`
	Status               string   `json:"status"`
	Freshness            string   `json:"freshness"`
	Gaps                 []string `json:"gaps"`
	InstructionsIncluded bool     `json:"instructions_included"`
}

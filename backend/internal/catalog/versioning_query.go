package catalog

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// The same selectors are used by HTTP, the assistant and native read methods.
// Filtering happens before pagination and does not broaden object visibility.
type VersionReadQuery struct {
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Target   string `json:"target,omitempty"`
	RecordID string `json:"record_id,omitempty"`
}

type VersionHistoryQuery struct {
	VersionReadQuery
	AuthorID string `json:"author_id,omitempty"`
	Since    string `json:"since,omitempty"`
	Until    string `json:"until,omitempty"`
	State    string `json:"state,omitempty"`
}

func (q VersionReadQuery) normalized() (VersionReadQuery, error) {
	if q.Target != "" && !contains([]string{"entity", "relation"}, q.Target) || q.RecordID != "" && !collaborationID(q.RecordID) {
		return q, fmt.Errorf("invalid_version_target")
	}
	if q.Limit < 0 || q.Limit > 100 {
		return q, fmt.Errorf("invalid_limit")
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Offset < 0 {
		return q, fmt.Errorf("invalid_offset")
	}
	return q, nil
}

func (q VersionReadQuery) matches(target, id string) bool {
	return (q.Target == "" || target == q.Target) && (q.RecordID == "" || id == q.RecordID)
}

func (q VersionHistoryQuery) normalized(branch bool) (VersionHistoryQuery, error) {
	var err error
	q.VersionReadQuery, err = q.VersionReadQuery.normalized()
	if err != nil {
		return q, err
	}
	if q.AuthorID != "" && !collaborationID(q.AuthorID) {
		return q, fmt.Errorf("invalid_author_id")
	}
	var since, until time.Time
	if q.Since != "" {
		since, err = time.Parse(time.RFC3339Nano, q.Since)
		if err != nil {
			return q, fmt.Errorf("invalid_time_range")
		}
	}
	if q.Until != "" {
		until, err = time.Parse(time.RFC3339Nano, q.Until)
		if err != nil {
			return q, fmt.Errorf("invalid_time_range")
		}
	}
	if !since.IsZero() && !until.IsZero() && !until.After(since) {
		return q, fmt.Errorf("invalid_time_range")
	}
	if branch {
		if q.State == "" {
			q.State = "active"
		}
		if !contains([]string{"active", "pending", "integrated", "replaced", "all"}, q.State) {
			return q, fmt.Errorf("invalid_history_state")
		}
	} else if q.State != "" {
		return q, fmt.Errorf("invalid_history_state")
	}
	return q, nil
}

// SQL fragments contain only fixed identifiers and numbered parameters.
func (q VersionHistoryQuery) sqlFilters(args *[]any, timeColumn string) string {
	clauses := []string{}
	bind := func(v any) string { *args = append(*args, v); return "$" + strconv.Itoa(len(*args)) }
	if q.Target != "" || q.RecordID != "" {
		parts := []string{"o.commit_id=c.id"}
		if q.Target != "" {
			parts = append(parts, "o.target="+bind(q.Target))
		}
		if q.RecordID != "" {
			parts = append(parts, "o.target_id="+bind(q.RecordID)+"::uuid")
		}
		clauses = append(clauses, "EXISTS(SELECT 1 FROM catalog.commit_objects o WHERE "+strings.Join(parts, " AND ")+")")
	}
	if q.AuthorID != "" {
		clauses = append(clauses, "EXISTS(SELECT 1 FROM catalog.commit_authors a WHERE a.commit_id=c.id AND a.user_id="+bind(q.AuthorID)+")")
	}
	if q.Since != "" {
		clauses = append(clauses, timeColumn+">="+bind(q.Since)+"::timestamptz")
	}
	if q.Until != "" {
		clauses = append(clauses, timeColumn+"<"+bind(q.Until)+"::timestamptz")
	}
	if len(clauses) == 0 {
		return ""
	}
	return " AND " + strings.Join(clauses, " AND ")
}

func versionReadQuery(c *gin.Context) (VersionReadQuery, error) {
	if err := versionTextQuery(c, "limit", "offset", "page", "target", "record_id"); err != nil {
		return VersionReadQuery{}, err
	}
	limit, offset, err := collaborationPagination(c)
	if err != nil {
		return VersionReadQuery{}, err
	}
	return (VersionReadQuery{Limit: limit, Offset: offset, Target: c.Query("target"), RecordID: c.Query("record_id")}).normalized()
}

func versionHistoryQuery(c *gin.Context, branch bool) (VersionHistoryQuery, error) {
	q, err := versionReadQuery(c)
	if err != nil {
		return VersionHistoryQuery{}, err
	}
	if err = versionTextQuery(c, "author_id", "since", "until", "state"); err != nil {
		return VersionHistoryQuery{}, err
	}
	return (VersionHistoryQuery{VersionReadQuery: q, AuthorID: c.Query("author_id"), Since: c.Query("since"), Until: c.Query("until"), State: c.Query("state")}).normalized(branch)
}

func versionTextQuery(c *gin.Context, names ...string) error {
	for _, name := range names {
		if len(c.Request.URL.Query()[name]) > 1 {
			return fmt.Errorf("invalid_query_param")
		}
	}
	return validateTextQuery(c, names...)
}

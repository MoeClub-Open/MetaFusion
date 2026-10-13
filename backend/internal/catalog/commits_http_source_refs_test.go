package catalog

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRespondCommitSourceRefsRequiredIncludesOperation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondCommit(c, CommitReceipt{}, &commitOperationError{Index: 2, Cause: errors.New("source_refs_required")})
	if w.Code != 400 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Error     string `json:"error"`
		Applied   *bool  `json:"applied"`
		Operation *int   `json:"operation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "source_refs_required" || body.Applied == nil || *body.Applied || body.Operation == nil || *body.Operation != 2 {
		t.Fatalf("want structured source_refs_required with operation=2, got %+v body=%s", body, w.Body.String())
	}
}

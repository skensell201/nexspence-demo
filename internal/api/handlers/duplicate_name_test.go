package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A duplicate name is a conflict the caller can act on, so every catalog
// resource answers it as one — with a message of its own rather than the
// driver's, whose text names the constraint and the table behind it.
//
// The repositories translate the unique violation (see translateNameUnique in
// internal/repository/postgres); these tests start from that translated error,
// which is what a handler actually sees.

var duplicateName error = &repository.UniqueViolationError{Field: "name"}

// assertNameConflict asserts a 409 whose body says only that the name is taken.
func assertNameConflict(t *testing.T, code int, body []byte) {
	t.Helper()
	require.Equal(t, http.StatusConflict, code, "body=%s", body)
	var got map[string]string
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "name already exists", got["error"])
	assert.NotContains(t, got["error"], "SQLSTATE")
	assert.NotContains(t, got["error"], "constraint")
}

func TestRoleHandler_Create_DuplicateName_409(t *testing.T) {
	r, roles, _ := mountRoles(t)
	roles.Err = duplicateName
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/roles",
		map[string]any{"name": "dev"})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestRoleHandler_Update_DuplicateName_409(t *testing.T) {
	r, roles, _ := mountRoles(t)
	ro := &domain.Role{Name: "ops"}
	require.NoError(t, roles.Create(testContext(), ro))
	roles.Err = duplicateName
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/roles/"+ro.ID,
		map[string]any{"name": "dev"})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestPrivilegeHandler_Create_DuplicateName_409(t *testing.T) {
	repo := testutil.NewPrivilegeRepo()
	h := handlers.NewPrivilegeHandler(repo, testutil.NewRoleRepo())
	r := gin.New()
	r.POST("/privileges", h.Create)
	repo.Err = duplicateName
	rec := do(t, r, http.MethodPost, "/privileges",
		map[string]any{"name": "read-all", "type": "repository-view"})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestPrivilegeHandler_Update_DuplicateName_409(t *testing.T) {
	repo := testutil.NewPrivilegeRepo()
	h := handlers.NewPrivilegeHandler(repo, testutil.NewRoleRepo())
	r := gin.New()
	r.PUT("/privileges/:id", h.Update)
	repo.Err = duplicateName
	rec := do(t, r, http.MethodPut, "/privileges/p1",
		map[string]any{"name": "read-all", "type": "repository-view"})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestBlobStoreHandler_Create_DuplicateName_409(t *testing.T) {
	r, blobRepo, _, _, _ := mountBlobStores(t)
	blobRepo.Err = duplicateName
	rec := do(t, r, http.MethodPost, "/service/rest/v1/blobstores/local",
		map[string]any{"name": "default"})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestContentSelectorHandler_Create_DuplicateName_409(t *testing.T) {
	r, repo := buildSelectorRouter(t)
	repo.Err = duplicateName
	rec := do(t, r, http.MethodPost, "/cs",
		map[string]any{"name": "mvn", "expression": `format == "maven2"`})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestContentSelectorHandler_Update_DuplicateName_409(t *testing.T) {
	r, repo := buildSelectorRouter(t)
	repo.Err = duplicateName
	rec := do(t, r, http.MethodPut, "/cs/cs-1",
		map[string]any{"name": "mvn", "expression": `format == "maven2"`})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestRoutingRuleHandler_Create_DuplicateName_409(t *testing.T) {
	r, repo := mountRoutingRules(t)
	repo.Err = duplicateName
	rec := do(t, r, http.MethodPost, "/service/rest/v1/routing-rules",
		map[string]any{"name": "block-npm", "mode": "BLOCK", "matchers": []string{".*"}})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

func TestRoutingRuleHandler_Update_DuplicateName_409(t *testing.T) {
	r, repo := mountRoutingRules(t)
	rule := &domain.RoutingRule{Name: "allow-all", Mode: "ALLOW", Matchers: []string{".*"}}
	require.NoError(t, repo.Create(testContext(), rule))
	repo.Err = duplicateName
	rec := do(t, r, http.MethodPut, "/service/rest/v1/routing-rules/"+rule.ID,
		map[string]any{"name": "block-npm", "mode": "BLOCK", "matchers": []string{".*"}})
	assertNameConflict(t, rec.Code, rec.Body.Bytes())
}

// A failure that is not a conflict keeps its old status: the 409 is reserved
// for the one case a caller can fix by choosing another name.
func TestRoleHandler_Create_OtherRepoError_Still500(t *testing.T) {
	r, roles, _ := mountRoles(t)
	roles.Err = service.ErrNotFound
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/roles",
		map[string]any{"name": "dev"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

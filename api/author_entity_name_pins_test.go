package api

import (
	"fmt"
	"net/http"
	"testing"

	"gopds-api/database"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A name pin names entities and the name each is pinned to; a malformed
// request reaches no service, and the store's refusals are closed codes.
func TestTheNamePinRoutes(t *testing.T) {
	svc := &fakeEntityAdmin{}
	r := entityRouter(svc)
	id := uuid.MustParse("01a17dc0-a8b4-7706-b3db-57951f632b68")

	rec := runsRequest(t, r, http.MethodGet, entityBase+"/names/"+id.String(), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"id":"01a17dc0-a8b4-7706-b3db-57951f632b68","display_name":"Урсула Крёбер Ле Гуин",
		"name_pinned":false,"forms":[{"name":"Урсула Ле Гуин","sort_name":"Ле Гуин, Урсула","books":198}]}`,
		rec.Body.String())

	rec = csrfRequest(t, r, http.MethodPost, entityBase+"/names/pins",
		fmt.Sprintf(`{"pins":[{"entity_id":%q,"name":"Урсула Ле Гуин"}]}`, id))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"entities":1}`, rec.Body.String())
	assert.Equal(t, adminActorID, svc.actor)

	rec = csrfRequest(t, r, http.MethodPost, entityBase+"/names/pins/revoke", fmt.Sprintf(`{"entities":[%q]}`, id))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"entities":1}`, rec.Body.String())
	assert.Equal(t, []string{"names " + id.String(), fmt.Sprintf("pin names [{%s Урсула Ле Гуин}]", id),
		fmt.Sprintf("unpin names [%s]", id)}, svc.calls)

	// Malformed: a path that is no UUID, an unknown field, a bad UUID.
	svc.calls = nil
	assert.Equal(t, http.StatusBadRequest, runsRequest(t, r, http.MethodGet, entityBase+"/names/42", "").Code)
	assert.Equal(t, http.StatusBadRequest, csrfRequest(t, r, http.MethodPost, entityBase+"/names/pins",
		`{"pins":[],"note":"x"}`).Code)
	assert.Equal(t, http.StatusBadRequest, csrfRequest(t, r, http.MethodPost, entityBase+"/names/pins/revoke",
		`{"entities":["no"]}`).Code)
	assert.Empty(t, svc.calls)

	for _, c := range []struct {
		err  error
		code int
		body string
	}{
		{database.ErrNamePinBatch, http.StatusUnprocessableEntity, codeNamePinBatch},
		{fmt.Errorf("%w: entity x", database.ErrNamePinEntity), http.StatusUnprocessableEntity, codeNamePinEntity},
		{fmt.Errorf("%w: entity x", database.ErrNamePinName), http.StatusUnprocessableEntity, codeNamePinName},
		{fmt.Errorf("%w: entity x", database.ErrNamePinNotPinned), http.StatusConflict, codeNamePinNotPinned},
		{database.ErrAuthorEntityBuildActive, http.StatusConflict, codeActiveRunExists},
	} {
		svc.err = c.err
		rec = csrfRequest(t, r, http.MethodPost, entityBase+"/names/pins",
			fmt.Sprintf(`{"pins":[{"entity_id":%q,"name":"Урсула"}]}`, id))
		assert.Equal(t, c.code, rec.Code, c.err.Error())
		assert.Contains(t, rec.Body.String(), c.body)
		rec = csrfRequest(t, r, http.MethodPost, entityBase+"/names/pins/revoke", fmt.Sprintf(`{"entities":[%q]}`, id))
		assert.Equal(t, c.code, rec.Code, c.err.Error())
	}
	svc.err = database.ErrNamePinEntity
	assert.Equal(t, http.StatusUnprocessableEntity,
		runsRequest(t, r, http.MethodGet, entityBase+"/names/"+id.String(), "").Code)
}

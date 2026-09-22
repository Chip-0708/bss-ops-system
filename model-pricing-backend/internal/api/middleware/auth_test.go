package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"model_bss/internal/domain/auth"
	"model_bss/pkg/apperr"
)

// ------- stub SessionStore -------

type stubSessionStore struct {
	sessions map[string]*SessionWithSnapshot
	refresh  map[int64]time.Time // sessionID → 新过期时间
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{
		sessions: map[string]*SessionWithSnapshot{},
		refresh:  map[int64]time.Time{},
	}
}

func (s *stubSessionStore) FindSessionByTokenHash(ctx context.Context, tokenHash string) (*SessionWithSnapshot, error) {
	if v, ok := s.sessions[tokenHash]; ok {
		return v, nil
	}
	return nil, nil
}

func (s *stubSessionStore) RefreshSessionExpiry(ctx context.Context, sessionID int64, newExpiresAt time.Time) error {
	s.refresh[sessionID] = newExpiresAt
	return nil
}

func sessionWithPerms(perms []string, expires time.Time) *SessionWithSnapshot {
	return &SessionWithSnapshot{
		ID:        1,
		Revoked:   false,
		ExpiresAt: expires,
		Snapshot: &auth.Snapshot{
			AccountID:    1,
			PortalType:   "INTERNAL",
			OperatorType: "STAFF",
			OperatorID:   10,
			StaffID:      10,
			MyOrgID:      7,
			Roles: []auth.RoleSnap{
				{Code: "PROCUREMENT", IsFunctional: false, DataScope: auth.ScopeDEPT},
			},
			Perms:      perms,
			DataScope:  auth.ScopeDEPT,
			ScopePaths: []string{"/1/2/"},
		},
	}
}

// ------- 用例 -------

func TestAuthN_MissingHeaderReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthN(newStubSessionStore()))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := performRequest(r, http.MethodGet, "/ping")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthN_WrongTokenReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newStubSessionStore()
	r := gin.New()
	r.Use(AuthN(store))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Authorization", "Bearer deadbeef")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthN_ExpiredSessionReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newStubSessionStore()
	store.sessions[tokenHash("expired-token")] = sessionWithPerms([]string{"M1:V"}, time.Now().Add(-time.Minute))

	r := gin.New()
	r.Use(AuthN(store))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Authorization", "Bearer expired-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthN_RevokedSessionReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newStubSessionStore()
	sess := sessionWithPerms([]string{"M1:V"}, time.Now().Add(time.Hour))
	sess.Revoked = true
	store.sessions[tokenHash("revoked-token")] = sess

	r := gin.New()
	r.Use(AuthN(store))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Authorization", "Bearer revoked-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthN_ValidTokenInjectsOperator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newStubSessionStore()
	store.sessions[tokenHash("valid-token")] = sessionWithPerms([]string{"M1:V"}, time.Now().Add(5*time.Hour))

	r := gin.New()
	r.Use(AuthN(store))
	r.GET("/who", func(c *gin.Context) {
		op := OperatorFrom(c)
		require.NotNil(t, op, "operator should be injected")
		require.Equal(t, int64(10), op.OperatorID)
		require.Equal(t, auth.ScopeDEPT, op.DataScope)
		require.Equal(t, []string{"/1/2/"}, op.ScopePaths)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/who", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, store.refresh, "no refresh when remaining > 2h")
}

func TestAuthN_SlidingRefreshWhenRemainingUnder2h(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newStubSessionStore()
	sess := sessionWithPerms([]string{"M1:V"}, time.Now().Add(30*time.Minute)) // 剩余 < 2h
	store.sessions[tokenHash("near-expire")] = sess

	r := gin.New()
	r.Use(AuthN(store))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Authorization", "Bearer near-expire")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, store.refresh, "session should be refreshed when remaining < 2h")
}

func TestAuthZ_MissingPermReturns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newStubSessionStore()
	store.sessions[tokenHash("no-m4")] = sessionWithPerms([]string{"M1:V"}, time.Now().Add(time.Hour))

	r := gin.New()
	r.Use(AuthN(store))
	rg := r.Group("")
	rg.Use(RequirePerm("M4:A"))
	rg.GET("/quote", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/quote", nil)
	req.Header.Set("Authorization", "Bearer no-m4")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAuthZ_HasPermReturns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newStubSessionStore()
	store.sessions[tokenHash("has-m4")] = sessionWithPerms([]string{"M1:V", "M4:A"}, time.Now().Add(time.Hour))

	r := gin.New()
	r.Use(AuthN(store))
	rg := r.Group("")
	rg.Use(RequirePerm("M4:A"))
	rg.GET("/quote", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/quote", nil)
	req.Header.Set("Authorization", "Bearer has-m4")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthZ_NoAuthNReturns401Not403(t *testing.T) {
	// 未经过 AuthN 直接命中 AuthZ：应 401（未认证优先于未授权）
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rg := r.Group("")
	rg.Use(RequirePerm("M4:A"))
	rg.GET("/quote", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := performRequest(r, http.MethodGet, "/quote")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, apperr.ErrUnauthorized.Code, 10002)
}

// performRequest 无头请求。
func performRequest(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

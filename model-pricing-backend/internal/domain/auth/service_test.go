package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"golang.org/x/crypto/bcrypt"

	"model_bss/pkg/apperr"
)

type fakeStore struct {
	account     *Account
	roles       []RoleGranted
	permsByRole map[int64][]string
	leaderNodes []OrgNode
	staff       *StaffRow
	sessions    map[string]*Session
	revoked     map[string]bool
	errFind     error
	errCreate   error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		permsByRole: map[int64][]string{},
		sessions:    map[string]*Session{},
		revoked:     map[string]bool{},
	}
}

func (f *fakeStore) FindAccountByLogin(ctx context.Context, portal, loginID string) (*Account, error) {
	if f.errFind != nil {
		return nil, f.errFind
	}
	if f.account != nil && f.account.PortalType == portal && f.account.LoginID == loginID {
		return f.account, nil
	}
	return nil, nil
}

func (f *fakeStore) ListRolesByOperator(ctx context.Context, operatorType string, operatorID int64) ([]RoleGranted, error) {
	return f.roles, nil
}

func (f *fakeStore) ListPermPointsByRole(ctx context.Context, roleID int64) ([]string, error) {
	return f.permsByRole[roleID], nil
}

func (f *fakeStore) ListOrgNodesByLeader(ctx context.Context, staffID int64) ([]OrgNode, error) {
	return f.leaderNodes, nil
}

func (f *fakeStore) FindStaff(ctx context.Context, staffID int64) (*StaffRow, error) {
	return f.staff, nil
}

func (f *fakeStore) CreateSession(ctx context.Context, s *Session) error {
	if f.errCreate != nil {
		return f.errCreate
	}
	f.sessions[s.TokenHash] = s
	return nil
}

func (f *fakeStore) RevokeSession(ctx context.Context, tokenHash string) error {
	f.revoked[tokenHash] = true
	if s, ok := f.sessions[tokenHash]; ok {
		s.Revoked = true
	}
	return nil
}

// fakeLimiter 简单内存计数。
type fakeLimiter struct {
	counts map[string]int
}

func newFakeLimiter() *fakeLimiter {
	return &fakeLimiter{counts: map[string]int{}}
}

func (l *fakeLimiter) Incr(key string, ttl time.Duration) int {
	l.counts[key]++
	return l.counts[key]
}

func (l *fakeLimiter) Get(key string) int {
	return l.counts[key]
}

func (l *fakeLimiter) Reset(key string) {
	delete(l.counts, key)
}

// ------- 测试数据 -------

var (
	hashOf    = func(pw string) string { h, _ := hashForTest(pw); return h }
	staffAcct = &Account{
		ID: 1, PortalType: "INTERNAL", OwnerType: "STAFF", OwnerID: 10,
		LoginID: "ops1", Status: "ACTIVE",
	}
)

// ------- 用例 -------

func TestLogin_SuccessWithSnapshot(t *testing.T) {
	store := newFakeStore()
	store.account = &Account{
		ID: 1, PortalType: "INTERNAL", OwnerType: "STAFF", OwnerID: 10,
		LoginID: "ops1", PasswordHash: hashOf("secret"), Status: "ACTIVE",
	}
	store.roles = []RoleGranted{
		{Role: RoleRow{ID: 2, Code: "PROCUREMENT", DataScope: ScopeDEPT, IsFunctional: false, FieldMask: []string{}}},
	}
	store.permsByRole[2] = []string{"M4:A", "M3:V", "M4:E"}
	store.staff = &StaffRow{ID: 10, OrgUnitID: 7}
	store.leaderNodes = []OrgNode{{ID: 7, Path: "/1/2/"}}

	svc := NewService(store, newFakeLimiter(), Config{})
	res, err := svc.Login(context.Background(), "INTERNAL", "ops1", "secret", "127.0.0.1")
	require.NoError(t, err)
	require.NotEmpty(t, res.Token)
	require.Len(t, res.Token, 64, "token should be 32-byte hex")

	snap := res.Snapshot
	require.Equal(t, int64(1), snap.AccountID)
	require.Equal(t, "STAFF", snap.OperatorType)
	require.Equal(t, int64(10), snap.StaffID)
	require.Equal(t, int64(7), snap.MyOrgID)
	require.ElementsMatch(t, []string{"M4:A", "M3:V", "M4:E"}, snap.Perms)
	// 非职能 + 负责人 + 声明 DEPT → min(DEPT, DEPT_SUB) = DEPT
	require.Equal(t, ScopeDEPT, snap.DataScope)
	require.Equal(t, []string{"/1/2/"}, snap.ScopePaths)

	// 库中只存 hash，且明文不在任何持久化字段
	require.NotEqual(t, res.Token, snap.AccountID)
	require.Equal(t, 1, len(store.sessions), "session persisted")
	for h := range store.sessions {
		require.NotEqual(t, res.Token, h, "raw token must not be stored")
	}
}

func TestLogin_WrongPasswordIncrementsFailCounter(t *testing.T) {
	store := newFakeStore()
	store.account = staffAcct
	store.account.PasswordHash = hashOf("right")
	limiter := newFakeLimiter()
	svc := NewService(store, limiter, Config{})

	for i := 0; i < 5; i++ {
		_, err := svc.Login(context.Background(), "INTERNAL", "ops1", "wrong", "127.0.0.1")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	// 第 6 次应被锁定 → 429
	_, err := svc.Login(context.Background(), "INTERNAL", "ops1", "right", "127.0.0.1")
	require.ErrorIs(t, err, ErrLoginLocked)
	require.Equal(t, apperr.ErrTooManyRequests.Status, ToAppErr(err).Status)
}

func TestLogin_SuccessResetsFailCounter(t *testing.T) {
	store := newFakeStore()
	store.account = &Account{
		ID: 1, PortalType: "INTERNAL", OwnerType: "STAFF", OwnerID: 10,
		LoginID: "ops1", PasswordHash: hashOf("right"), Status: "ACTIVE",
	}
	limiter := newFakeLimiter()
	svc := NewService(store, limiter, Config{})

	_, err := svc.Login(context.Background(), "INTERNAL", "ops1", "wrong", "127.0.0.1")
	require.ErrorIs(t, err, ErrBadCredentials)
	key := "login_fail:INTERNAL:ops1"
	require.Equal(t, 1, limiter.counts[key])

	_, err = svc.Login(context.Background(), "INTERNAL", "ops1", "right", "127.0.0.1")
	require.NoError(t, err)
	require.Equal(t, 0, limiter.counts[key], "success must reset counter")
}

func TestLogin_UnknownAccountDoesNotCountLoginIDButCountsIP(t *testing.T) {
	store := newFakeStore() // account=nil（账号不存在）
	limiter := newFakeLimiter()
	svc := NewService(store, limiter, Config{})

	for i := 0; i < 3; i++ {
		_, err := svc.Login(context.Background(), "INTERNAL", "ghost", "pw", "10.0.0.1")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	// login_id 维度：不计数（防止随机 login_id 打爆缓存）
	require.Equal(t, 0, limiter.counts["login_fail:INTERNAL:ghost"])
	// IP 维度：累计 3 次
	require.Equal(t, 3, limiter.counts["login_fail_ip:INTERNAL:10.0.0.1"])
}

func TestLogin_IPSoftLimitBlocksAfter50(t *testing.T) {
	store := newFakeStore() // account=nil
	limiter := newFakeLimiter()
	svc := NewService(store, limiter, Config{})

	for i := 0; i < 50; i++ {
		_, err := svc.Login(context.Background(), "INTERNAL", "ghost", "pw", "10.0.0.2")
		require.ErrorIs(t, err, ErrBadCredentials)
	}
	// 第 51 次：IP 超限 → 429
	_, err := svc.Login(context.Background(), "INTERNAL", "anyone", "pw", "10.0.0.2")
	require.ErrorIs(t, err, ErrLoginLocked)
}

func TestLogin_AccountFrozenReturns423(t *testing.T) {
	store := newFakeStore()
	store.account = &Account{
		ID: 1, PortalType: "SUPPLIER", OwnerType: "OPERATOR", OwnerID: 20,
		LoginID: "sup1", PasswordHash: hashOf("pw"), Status: "ACTIVE",
		SubjectFlag: "QUAL", SubjectStatus: "FROZEN",
	}
	svc := NewService(store, newFakeLimiter(), Config{})
	_, err := svc.Login(context.Background(), "SUPPLIER", "sup1", "pw", "127.0.0.1")
	require.ErrorIs(t, err, ErrAccountFrozen)
	require.Equal(t, apperr.ErrSubjectFrozen.Status, ToAppErr(err).Status)
	require.Equal(t, 423, ToAppErr(err).Status)
}

func TestLogin_AccountSuspendedReturns423(t *testing.T) {
	store := newFakeStore()
	store.account = &Account{
		ID: 1, PortalType: "INTERNAL", OwnerType: "STAFF", OwnerID: 10,
		LoginID: "off1", PasswordHash: hashOf("pw"), Status: "SUSPENDED",
	}
	svc := NewService(store, newFakeLimiter(), Config{})
	_, err := svc.Login(context.Background(), "INTERNAL", "off1", "pw", "127.0.0.1")
	require.ErrorIs(t, err, ErrAccountFrozen)
}
func TestLogin_UnknownAccountIsBadCredentials(t *testing.T) {
	store := newFakeStore() // account=nil
	svc := NewService(store, newFakeLimiter(), Config{})
	_, err := svc.Login(context.Background(), "INTERNAL", "ghost", "pw", "127.0.0.1")
	require.ErrorIs(t, err, ErrBadCredentials)
}

func TestLogin_FunctionalRoleGrantsALL(t *testing.T) {
	store := newFakeStore()
	store.account = &Account{
		ID: 1, PortalType: "INTERNAL", OwnerType: "STAFF", OwnerID: 10,
		LoginID: "pricer", PasswordHash: hashOf("pw"), Status: "ACTIVE",
	}
	store.roles = []RoleGranted{
		{Role: RoleRow{ID: 3, Code: "PRICING_OP", DataScope: ScopeALL, IsFunctional: true}},
	}
	store.permsByRole[3] = []string{"M5:E", "M6:V", "M7:E"}
	store.staff = &StaffRow{ID: 10, OrgUnitID: 1}
	svc := NewService(store, newFakeLimiter(), Config{})

	res, err := svc.Login(context.Background(), "INTERNAL", "pricer", "pw", "127.0.0.1")
	require.NoError(t, err)
	// 职能角色即使非负责人也必须是 ALL
	require.Equal(t, ScopeALL, res.Snapshot.DataScope)
	require.Empty(t, res.Snapshot.ScopePaths)
}

func TestLogout_RevokesByTokenHash(t *testing.T) {
	store := newFakeStore()
	// 用共享 staffAcct 前先复制，避免字段被多个用例相互污染（staffAcct 本身无 PasswordHash）。
	acct := *staffAcct
	store.account = &acct
	store.account.PasswordHash = hashOf("pw")
	store.staff = &StaffRow{ID: 10, OrgUnitID: 1}
	svc := NewService(store, newFakeLimiter(), Config{})

	res, err := svc.Login(context.Background(), "INTERNAL", "ops1", "pw", "127.0.0.1")
	require.NoError(t, err)
	h := tokenHash(res.Token)
	_, ok := store.sessions[h]
	require.True(t, ok, "session should exist before logout")

	require.NoError(t, svc.Logout(context.Background(), res.Token))
	require.True(t, store.revoked[h], "session should be revoked")
}

// ------- helpers -------

func hashForTest(pw string) (string, error) {
	return bcryptHash(pw)
}

// bcryptHash 包装 bcrypt 生成测试用哈希。
func bcryptHash(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	return string(b), err
}

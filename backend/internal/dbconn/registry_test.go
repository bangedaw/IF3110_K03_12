package dbconn_test

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbconn"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
)

const testKey = "REVWLU9OTFktREVWLU9OTFktREVWLU9OTFktREVWLU8="

type fakeConn struct{ closed atomic.Bool }

func (c *fakeConn) Ping(context.Context) error { return nil }
func (*fakeConn) Inspector() dbms.Inspector    { return nil }
func (*fakeConn) Browser() dbms.DataBrowser    { return nil }
func (*fakeConn) Migrator() dbms.Migrator      { return dbms.UnimplementedMigrator(true) }
func (*fakeConn) Explainer() dbms.Explainer    { return dbms.UnimplementedExplainer() }
func (c *fakeConn) Close() error               { c.closed.Store(true); return nil }

type fakeDriver struct {
	opens atomic.Int32
	err   error
	conns []*fakeConn
	mu    sync.Mutex
}

func (d *fakeDriver) Kind() string { return "fake" }

func (d *fakeDriver) Open(context.Context, dbms.ConnConfig) (dbms.Conn, error) {
	d.opens.Add(1)
	if d.err != nil {
		return nil, d.err
	}
	c := &fakeConn{}
	d.mu.Lock()
	d.conns = append(d.conns, c)
	d.mu.Unlock()
	return c, nil
}

type fakeStore struct {
	rows map[int64]dbconn.TargetCredentials
	err  error
}

func (s fakeStore) GetConnection(context.Context, int64) (dbconn.TargetCredentials, error) {
	if s.err != nil {
		return dbconn.TargetCredentials{}, s.err
	}
	var out dbconn.TargetCredentials
	for _, r := range s.rows {
		out = r
	}
	return out, nil
}

func newRegistry(t *testing.T, store dbconn.RegistryStore, driver dbms.Driver) *dbconn.Registry {
	t.Helper()
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	return &dbconn.Registry{
		Store:   store,
		Cipher:  cipher,
		Drivers: func(string) (dbms.Driver, bool) { return driver, true },
	}
}

func row(t *testing.T, id int64) dbconn.TargetCredentials {
	t.Helper()
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	return dbconn.TargetCredentials{
		ID:                 id,
		Name:               "inventory",
		Kind:               "fake",
		Host:               "db.internal",
		Port:               5432,
		Database:           "inventory",
		User:               "app",
		PasswordCiphertext: cipher.Encrypt([]byte("rahasia")),
		SSLMode:            "require",
		IsActive:           true,
	}
}

func TestGetCachesConnection(t *testing.T) {
	driver := &fakeDriver{}
	store := fakeStore{rows: map[int64]dbconn.TargetCredentials{7: row(t, 7)}}
	reg := newRegistry(t, store, driver)

	ctx := context.Background()
	a, err := reg.Get(ctx, 7)
	require.NoError(t, err)
	b, err := reg.Get(ctx, 7)
	require.NoError(t, err)

	require.Same(t, a, b, "Get kedua harus memakai koneksi yang sama")
	require.EqualValues(t, 1, driver.opens.Load(), "driver.Open hanya boleh dipanggil sekali")
}

func TestGetIsConcurrencySafe(t *testing.T) {
	driver := &fakeDriver{}
	store := fakeStore{rows: map[int64]dbconn.TargetCredentials{7: row(t, 7)}}
	reg := newRegistry(t, store, driver)

	var wg sync.WaitGroup
	conns := make([]dbms.Conn, 50)
	errs := make([]error, 50)
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conns[i], errs[i] = reg.Get(context.Background(), 7)
		}()
	}
	wg.Wait()

	for i := range 50 {
		require.NoError(t, errs[i])
		require.Same(t, conns[0], conns[i])
	}
	require.EqualValues(t, 1, driver.opens.Load(), "50 Get paralel tetap satu Open")
}

func TestInvalidateClosesAndReopens(t *testing.T) {
	driver := &fakeDriver{}
	store := fakeStore{rows: map[int64]dbconn.TargetCredentials{7: row(t, 7)}}
	reg := newRegistry(t, store, driver)

	first, err := reg.Get(context.Background(), 7)
	require.NoError(t, err)

	reg.Invalidate(7)
	require.True(t, first.(*fakeConn).closed.Load(), "koneksi lama harus ditutup saat invalidate")

	second, err := reg.Get(context.Background(), 7)
	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.EqualValues(t, 2, driver.opens.Load())
}

func TestGetNotFound(t *testing.T) {
	reg := newRegistry(t, fakeStore{err: apperr.ErrNotFound}, &fakeDriver{})
	_, err := reg.Get(context.Background(), 7)
	require.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestGetFailedOpenIsNotCached(t *testing.T) {
	driver := &fakeDriver{err: errors.New("koneksi ditolak")}
	store := fakeStore{rows: map[int64]dbconn.TargetCredentials{7: row(t, 7)}}
	reg := newRegistry(t, store, driver)

	_, err := reg.Get(context.Background(), 7)
	require.Error(t, err)

	driver.err = nil // pulih; percobaan berikutnya harus mencoba lagi
	_, err = reg.Get(context.Background(), 7)
	require.NoError(t, err, "kegagalan open tidak boleh di-cache selamanya")
}

func TestCloseClosesEverything(t *testing.T) {
	driver := &fakeDriver{}
	store := fakeStore{rows: map[int64]dbconn.TargetCredentials{7: row(t, 7)}}
	reg := newRegistry(t, store, driver)

	conn, err := reg.Get(context.Background(), 7)
	require.NoError(t, err)

	reg.Close()
	require.True(t, conn.(*fakeConn).closed.Load())
}

func TestDecryptFailureDoesNotLeakPassword(t *testing.T) {
	bad := row(t, 7)
	bad.PasswordCiphertext = []byte("bukan-gcm")
	driver := &fakeDriver{}
	reg := newRegistry(t, fakeStore{rows: map[int64]dbconn.TargetCredentials{7: bad}}, driver)

	_, err := reg.Get(context.Background(), 7)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "rahasia")
	require.EqualValues(t, 0, driver.opens.Load(), "tidak boleh mencoba koneksi dengan kredensial rusak")
}

func TestKeyEncodingSanity(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(testKey)
	require.NoError(t, err)
	require.Len(t, raw, 32)
}

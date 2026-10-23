package dbconn

import (
	"context"
	"sync"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
)

type TargetCredentials struct {
	ID                 int64
	Name               string
	Kind               string
	Host               string
	Port               int
	Database           string
	User               string
	PasswordCiphertext []byte
	SSLMode            string
	IsActive           bool
}

type RegistryStore interface {
	GetConnection(ctx context.Context, id int64) (TargetCredentials, error)
}

type Registry struct {
	Store   RegistryStore
	Cipher  *secret.Cipher
	Drivers func(kind string) (dbms.Driver, bool)

	mu      sync.Mutex
	entries map[int64]*entry
}

type entry struct {
	once sync.Once
	conn dbms.Conn
	err  error
}

func NewRegistry(store RegistryStore, cipher *secret.Cipher) *Registry {
	return &Registry{
		Store:   store,
		Cipher:  cipher,
		Drivers: dbms.DriverFor,
		entries: map[int64]*entry{},
	}
}

func (r *Registry) Get(ctx context.Context, id int64) (dbms.Conn, error) {
	r.mu.Lock()
	if r.entries == nil {
		r.entries = map[int64]*entry{}
	}
	e, ok := r.entries[id]
	if !ok {
		e = &entry{}
		r.entries[id] = e
	}
	r.mu.Unlock()

	e.once.Do(func() { e.conn, e.err = r.open(ctx, id) })
	if e.err != nil {
		r.Invalidate(id)
		return nil, e.err
	}
	return e.conn, nil
}

func (r *Registry) Invalidate(id int64) {
	r.mu.Lock()
	e, ok := r.entries[id]
	delete(r.entries, id)
	r.mu.Unlock()

	if ok && e.conn != nil {
		_ = e.conn.Close()
	}
}

func (r *Registry) Close() {
	r.mu.Lock()
	entries := r.entries
	r.entries = map[int64]*entry{}
	r.mu.Unlock()

	for _, e := range entries {
		if e.conn != nil {
			_ = e.conn.Close()
		}
	}
}

func (r *Registry) open(ctx context.Context, id int64) (dbms.Conn, error) {
	row, err := r.Store.GetConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	if !row.IsActive {
		return nil, apperr.New(apperr.CodeNotFound, "koneksi dinonaktifkan")
	}
	driver, ok := r.Drivers(row.Kind)
	if !ok {
		return nil, apperr.New(apperr.CodeValidation, "DBMS "+row.Kind+" belum didukung")
	}
	password, err := r.Cipher.Decrypt(row.PasswordCiphertext)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeValidation, "kredensial koneksi tidak bisa dibaca; periksa DBMP_SECRET_KEY", err)
	}

	return driver.Open(ctx, dbms.ConnConfig{
		Kind:     row.Kind,
		Host:     row.Host,
		Port:     row.Port,
		Database: row.Database,
		User:     row.User,
		Password: string(password),
		SSLMode:  row.SSLMode,
	})
}

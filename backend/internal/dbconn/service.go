package dbconn

import (
	"context"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/apperr"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/platformdb/gen"
	"github.com/bangedaw/IF3110_K03_12/backend/internal/secret"
)

const testTimeout = 5 * time.Second

type Service struct {
	store      Store
	registry   *Registry
	cipher     *secret.Cipher
	authorizer authz.Authorizer
}

func NewService(store Store, registry *Registry, cipher *secret.Cipher, az authz.Authorizer) *Service {
	return &Service{store: store, registry: registry, cipher: cipher, authorizer: az}
}

type Connection struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	DatabaseName string    `json:"databaseName"`
	Username     string    `json:"username"`
	SSLMode      string    `json:"sslMode"`
	IsActive     bool      `json:"isActive"`
	HasPassword  bool      `json:"hasPassword"`
	CreatedByID  *int64    `json:"createdById,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Page struct {
	Items  []Connection `json:"items"`
	Limit  int32        `json:"limit"`
	Offset int32        `json:"offset"`
	Total  int64        `json:"total"`
}

type Input struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	DatabaseName string `json:"databaseName"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	SSLMode      string `json:"sslMode"`
	IsActive     *bool  `json:"isActive"`
}

type Patch struct {
	Name         *string `json:"name"`
	Kind         *string `json:"kind"`
	Host         *string `json:"host"`
	Port         *int    `json:"port"`
	DatabaseName *string `json:"databaseName"`
	Username     *string `json:"username"`
	Password     string  `json:"password"`
	SSLMode      *string `json:"sslMode"`
	IsActive     *bool   `json:"isActive"`
}

type TestResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Detail    string `json:"detail,omitempty"`
}

func (s *Service) List(ctx context.Context, p authz.Principal, limit, offset int32) (*Page, error) {
	if err := s.authorize(ctx, p); err != nil {
		return nil, err
	}
	rows, err := s.store.ListConnections(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	total, err := s.store.CountConnections(ctx)
	if err != nil {
		return nil, err
	}
	page := &Page{Items: make([]Connection, 0, len(rows)), Limit: limit, Offset: offset, Total: total}
	for _, row := range rows {
		page.Items = append(page.Items, toAPI(row))
	}
	return page, nil
}

func (s *Service) Get(ctx context.Context, p authz.Principal, id int64) (*Connection, error) {
	if err := s.authorize(ctx, p); err != nil {
		return nil, err
	}
	row, err := s.store.GetConnectionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	out := toAPI(row)
	return &out, nil
}

func (s *Service) Create(ctx context.Context, p authz.Principal, in Input) (*Connection, error) {
	if err := s.authorize(ctx, p); err != nil {
		return nil, err
	}
	if err := validate(in, true); err != nil {
		return nil, err
	}
	cipherText := s.cipher.Encrypt([]byte(in.Password))
	row, err := s.store.InsertConnection(ctx, gen.InsertConnectionParams{
		Name:               in.Name,
		Kind:               gen.DbmsKind(in.Kind),
		Host:               in.Host,
		Port:               int32(in.Port),
		DatabaseName:       in.DatabaseName,
		Username:           in.Username,
		PasswordCiphertext: cipherText,
		SslMode:            sslMode(in.SSLMode),
		IsActive:           in.IsActive == nil || *in.IsActive,
		CreatedBy:          nullableUserID(p.UserID),
	})
	if err != nil {
		return nil, err
	}
	out := toAPI(row)
	return &out, nil
}

func (s *Service) Update(ctx context.Context, p authz.Principal, id int64, in Patch) (*Connection, error) {
	if err := s.authorize(ctx, p); err != nil {
		return nil, err
	}
	current, err := s.store.GetConnectionByID(ctx, id)
	if err != nil {
		return nil, err
	}

	next := Input{
		Name:         current.Name,
		Kind:         string(current.Kind),
		Host:         current.Host,
		Port:         int(current.Port),
		DatabaseName: current.DatabaseName,
		Username:     current.Username,
		SSLMode:      current.SslMode,
		IsActive:     &current.IsActive,
	}
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.Kind != nil {
		next.Kind = *in.Kind
	}
	if in.Host != nil {
		next.Host = *in.Host
	}
	if in.Port != nil {
		next.Port = *in.Port
	}
	if in.DatabaseName != nil {
		next.DatabaseName = *in.DatabaseName
	}
	if in.Username != nil {
		next.Username = *in.Username
	}
	if in.SSLMode != nil {
		next.SSLMode = *in.SSLMode
	}
	if in.IsActive != nil {
		next.IsActive = in.IsActive
	}
	if err := validate(next, false); err != nil {
		return nil, err
	}

	updatePassword := in.Password != ""
	if updatePassword && len(in.Password) > 512 {
		return nil, apperr.Validation(map[string]string{"password": "maksimal 512 karakter"})
	}
	var cipherText []byte
	if updatePassword {
		cipherText = s.cipher.Encrypt([]byte(in.Password))
	}

	row, err := s.store.UpdateConnection(ctx, gen.UpdateConnectionParams{
		ID:                 id,
		Name:               next.Name,
		Kind:               gen.DbmsKind(next.Kind),
		Host:               next.Host,
		Port:               int32(next.Port),
		DatabaseName:       next.DatabaseName,
		Username:           next.Username,
		PasswordCiphertext: cipherText,
		SslMode:            sslMode(next.SSLMode),
		IsActive:           *next.IsActive,
		UpdatePassword:     updatePassword,
	})
	if err != nil {
		return nil, err
	}
	if updatePassword {
		s.registry.Invalidate(id)
	}
	out := toAPI(row)
	return &out, nil
}

func (s *Service) Delete(ctx context.Context, p authz.Principal, id int64) error {
	if err := s.authorize(ctx, p); err != nil {
		return err
	}
	if _, err := s.store.GetConnectionByID(ctx, id); err != nil {
		return err
	}
	s.registry.Invalidate(id)
	return s.store.DeleteConnection(ctx, id)
}

func (s *Service) Test(ctx context.Context, p authz.Principal, in Input, databaseID *int64) (*TestResult, error) {
	if err := s.authorize(ctx, p); err != nil {
		return nil, err
	}

	var cfg dbms.ConnConfig
	if databaseID != nil {
		row, err := s.store.GetConnectionByID(ctx, *databaseID)
		if err != nil {
			return nil, err
		}
		password, err := s.cipher.Decrypt(row.PasswordCiphertext)
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeValidation, "kredensial koneksi tidak bisa dibaca; periksa DBMP_SECRET_KEY", err)
		}
		cfg = dbms.ConnConfig{
			Kind: string(row.Kind), Host: row.Host, Port: int(row.Port),
			Database: row.DatabaseName, User: row.Username, Password: string(password), SSLMode: row.SslMode,
		}
	} else {
		if err := validate(in, true); err != nil {
			return nil, err
		}
		cfg = dbms.ConnConfig{
			Kind: in.Kind, Host: in.Host, Port: in.Port,
			Database: in.DatabaseName, User: in.Username, Password: in.Password, SSLMode: sslMode(in.SSLMode),
		}
	}

	driver, ok := dbms.DriverFor(cfg.Kind)
	if !ok {
		return nil, apperr.Validation(map[string]string{"kind": "DBMS belum didukung"})
	}

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	start := time.Now()
	conn, err := driver.Open(ctx, cfg)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeDBUnreachable, "database target tidak terjangkau", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.Ping(ctx); err != nil {
		return nil, apperr.Wrap(apperr.CodeDBUnreachable, "database target tidak merespons", err)
	}
	return &TestResult{OK: true, LatencyMs: time.Since(start).Milliseconds()}, nil
}

func (s *Service) authorize(ctx context.Context, p authz.Principal) error {
	return s.authorizer.Authorize(ctx, p, authz.ActionManageConnections, authz.Resource{})
}

func toAPI(row gen.DatabaseConnection) Connection {
	out := Connection{
		ID:           row.ID,
		Name:         row.Name,
		Kind:         string(row.Kind),
		Host:         row.Host,
		Port:         int(row.Port),
		DatabaseName: row.DatabaseName,
		Username:     row.Username,
		SSLMode:      row.SslMode,
		IsActive:     row.IsActive,
		HasPassword:  len(row.PasswordCiphertext) > 0,
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
	}
	if row.CreatedBy.Valid {
		id := row.CreatedBy.Int64
		out.CreatedByID = &id
	}
	return out
}

var validSSL = map[string]bool{
	"disable": true, "require": true, "verify-ca": true, "verify-full": true,
}

func sslMode(m string) string {
	if m == "" {
		return "disable"
	}
	return m
}

func validate(in Input, requirePassword bool) *apperr.Error {
	fields := map[string]string{}
	if n := len(in.Name); n < 1 || n > 64 {
		fields["name"] = "panjang 1-64 karakter"
	}
	if in.Kind != dbms.KindPostgres && in.Kind != dbms.KindMySQL {
		fields["kind"] = "harus postgres atau mysql"
	}
	if n := len(in.Host); n < 1 || n > 255 {
		fields["host"] = "wajib diisi, maksimal 255 karakter"
	}
	if in.Port < 1 || in.Port > 65535 {
		fields["port"] = "harus antara 1 dan 65535"
	}
	if n := len(in.DatabaseName); n < 1 || n > 128 {
		fields["databaseName"] = "wajib diisi, maksimal 128 karakter"
	}
	if n := len(in.Username); n < 1 || n > 128 {
		fields["username"] = "wajib diisi, maksimal 128 karakter"
	}
	if requirePassword && in.Password == "" {
		fields["password"] = "wajib diisi"
	}
	if len(in.Password) > 512 {
		fields["password"] = "maksimal 512 karakter"
	}
	if in.SSLMode != "" && !validSSL[in.SSLMode] {
		fields["sslMode"] = "harus disable, require, verify-ca, atau verify-full"
	}
	if len(fields) > 0 {
		return apperr.Validation(fields)
	}
	return nil
}

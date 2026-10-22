package apperr

type Code string

const (
	CodeUnauthenticated Code = "UNAUTHENTICATED"
	CodeSessionExpired  Code = "SESSION_EXPIRED"
	CodeForbidden       Code = "FORBIDDEN"
	CodeCSRFInvalid     Code = "CSRF_INVALID"
	CodeNotFound        Code = "NOT_FOUND"
	CodeValidation      Code = "VALIDATION_FAILED"
	CodeConflict        Code = "CONFLICT"
	CodeDBUnreachable   Code = "DB_UNREACHABLE"
	CodeMigrationFailed Code = "MIGRATION_FAILED"
)

type Error struct {
	Code   Code
	Detail string
	Fields map[string]string
	Err    error
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Detail
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

func New(code Code, detail string) *Error { return &Error{Code: code, Detail: detail} }

func Wrap(code Code, detail string, cause error) *Error {
	return &Error{Code: code, Detail: detail, Err: cause}
}

func Validation(fields map[string]string) *Error {
	return &Error{Code: CodeValidation, Detail: "input tidak valid", Fields: fields}
}

var (
	ErrUnauthenticated = New(CodeUnauthenticated, "")
	ErrForbidden       = New(CodeForbidden, "")
	ErrNotFound        = New(CodeNotFound, "")
	ErrValidation      = New(CodeValidation, "")
	ErrConflict        = New(CodeConflict, "")
	ErrDBUnreachable   = New(CodeDBUnreachable, "")
)

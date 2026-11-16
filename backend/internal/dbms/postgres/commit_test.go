package postgres

import (
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/dbms"
)

func TestCommitErrorWithoutServerAnswerIsUncertain(t *testing.T) {
	err := commitError(io.ErrUnexpectedEOF)

	assert.ErrorIs(t, err, dbms.ErrCommitUncertain)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
	var se *dbms.StatementError
	assert.False(t, errors.As(err, &se))
}

func TestCommitErrorRejectedByServerIsCertain(t *testing.T) {
	err := commitError(&pgconn.PgError{Code: "40001", Message: "could not serialize access"})

	assert.NotErrorIs(t, err, dbms.ErrCommitUncertain)
	var se *dbms.StatementError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, "40001", se.SQLState)
	assert.Equal(t, "commit failed: could not serialize access", se.Error())
}

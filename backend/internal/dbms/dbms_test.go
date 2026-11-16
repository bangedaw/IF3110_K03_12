package dbms

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectionNormalize(t *testing.T) {
	for in, want := range map[Direction]Direction{"": DirectionUp, "up": DirectionUp, "down": DirectionDown} {
		got, err := in.Normalize()
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []Direction{"UP", "rollback", " down"} {
		_, err := in.Normalize()
		assert.ErrorIs(t, err, ErrInvalidDirection, in)
	}
}

func TestStatementErrorMessage(t *testing.T) {
	assert.Equal(t, "statement 3 failed: boom", (&StatementError{Index: 3, Message: "boom"}).Error())
	assert.Equal(t, "commit failed: boom", (&StatementError{Message: "boom"}).Error())
	assert.Equal(t, 3, (&StatementError{Index: 3}).StatementIndex())
}

func TestRetryableSQLState(t *testing.T) {
	retry := []string{"40001", "40P01", "55P03", "57P03", "08000", "08006", "08P01", "53000", "53300"}
	for _, code := range retry {
		assert.True(t, RetryableSQLState(code), code)
		assert.True(t, (&StatementError{SQLState: code}).Retryable(), code)
	}
	final := []string{"", "57014", "42P01", "42601", "23505", "23503", "22012", "25P02", "XX000"}
	for _, code := range final {
		assert.False(t, RetryableSQLState(code), code)
		assert.False(t, (&StatementError{SQLState: code}).Retryable(), code)
	}
}

func TestBatchProgressState(t *testing.T) {
	cases := map[BatchState]BatchProgress{
		BatchPending:           {},
		BatchPartial:           {BatchesCommitted: 2},
		BatchCompleted:         {BatchesCommitted: 2, Completed: true},
		BatchPartiallyReverted: {BatchesCommitted: 2, Completed: true, BatchesReverted: 1},
		BatchReverted:          {BatchesCommitted: 2, BatchesReverted: 2, RevertCompleted: true},
	}
	for want, p := range cases {
		assert.Equal(t, want, p.State(), want)
	}
	assert.Equal(t, BatchCompleted, BatchProgress{Completed: true}.State(), "tabel kosong")
}

func TestStatementErrorMessageIncludesBatch(t *testing.T) {
	assert.Equal(t, "batch 6: statement 1 failed: boom", (&StatementError{Batch: 6, Index: 1, Message: "boom"}).Error())
	assert.Equal(t, "batch 6: commit failed: boom", (&StatementError{Batch: 6, Message: "boom"}).Error())
}

package web

import (
	"html/template"
	"net/url"
	"strings"
	"time"

	"github.com/bangedaw/IF3110_K03_12/backend/internal/authz"
)

var funcMap = template.FuncMap{
	"fmtTime":    formatTime,
	"levelLabel": levelLabel,
	"kindLabel":  kindLabel,
	"join":       strings.Join,
	"pathEscape": url.PathEscape,
	"isEdit":     func(l authz.Level) bool { return l == authz.LevelEdit },
	"hasPrefix":  strings.HasPrefix,
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func levelLabel(l authz.Level) string {
	switch l {
	case authz.LevelEdit:
		return "View + Edit"
	case authz.LevelView:
		return "View Only"
	default:
		return "Tanpa akses"
	}
}

func kindLabel(kind string) string {
	switch kind {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	default:
		return kind
	}
}

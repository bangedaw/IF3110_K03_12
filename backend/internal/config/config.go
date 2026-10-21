package config

import (
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	Env         string `env:"APP_ENV" envDefault:"dev"`
	HTTPAddr    string `env:"HTTP_ADDR" envDefault:":8080"`
	DatabaseURL string `env:"DATABASE_URL,required"`
	SecretKey   string `env:"DBMP_SECRET_KEY,required"`
	CSRFKey     string `env:"CSRF_KEY,required"`
	Session     Session
}

type Session struct {
	IdleTimeout      time.Duration `env:"SESSION_IDLE_TIMEOUT" envDefault:"30m"`
	AbsoluteTimeout  time.Duration `env:"SESSION_ABSOLUTE_TIMEOUT" envDefault:"8h"`
	RotationInterval time.Duration `env:"SESSION_ROTATION_INTERVAL" envDefault:"15m"`
	GracePeriod      time.Duration `env:"SESSION_GRACE_PERIOD" envDefault:"30s"`
	TouchInterval    time.Duration `env:"SESSION_TOUCH_INTERVAL" envDefault:"60s"`
}

func (c Config) IsProduction() bool { return c.Env == "production" }

func Load() (Config, error) {
	for _, f := range []string{".env", "../.env"} {
		_ = godotenv.Load(f)
	}
	return env.ParseAs[Config]()
}

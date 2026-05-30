package dbconfig

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	KeyDriver = "dbDriver"
	KeySource = "dbSource"

	DriverSQLite = "sqlite"
	DriverPGX    = "pgx"
)

type Getter func(key string) (string, error)

type Config struct {
	Driver string
	Source string
}

func Resolve(configPath string, getter Getter) (Config, error) {
	driver := ""
	source := ""
	var err error
	if getter != nil {
		driver, err = getter(KeyDriver)
		if err != nil {
			return Config{}, fmt.Errorf("read %s: %w", KeyDriver, err)
		}
		source, err = getter(KeySource)
		if err != nil {
			return Config{}, fmt.Errorf("read %s: %w", KeySource, err)
		}
	}

	cfg := Config{
		Driver: normalizeDriver(driver),
		Source: strings.TrimSpace(source),
	}

	switch cfg.Driver {
	case DriverSQLite:
		if cfg.Source == "" {
			cfg.Source = filepath.Join(filepath.Dir(configPath), "data.sqlite")
		} else if !filepath.IsAbs(cfg.Source) {
			cfg.Source = filepath.Join(filepath.Dir(configPath), cfg.Source)
		}
		cfg.Source = filepath.Clean(cfg.Source)
	case DriverPGX:
		if cfg.Source == "" {
			return Config{}, errors.New("dbSource is required when dbDriver=pgx")
		}
	default:
		return Config{}, fmt.Errorf("unsupported dbDriver %q", cfg.Driver)
	}

	return cfg, nil
}

func ResolveSource(configPath, source string) (Config, error) {
	source = strings.TrimSpace(source)
	driver := DriverSQLite

	scheme := sourceScheme(source)
	switch scheme {
	case "", "file":
		driver = DriverSQLite
	case "postgres", "postgresql":
		driver = DriverPGX
	case "mysql", "mysql2", "mariadb":
		return Config{}, fmt.Errorf("unsupported database DSN scheme %q", scheme)
	default:
		if strings.Contains(source, "://") {
			return Config{}, fmt.Errorf("unsupported database DSN scheme %q", scheme)
		}
		driver = DriverSQLite
	}
	return Resolve(configPath, func(key string) (string, error) {
		switch key {
		case KeyDriver:
			return driver, nil
		case KeySource:
			return source, nil
		default:
			return "", nil
		}
	})
}

func sourceScheme(source string) string {
	if !strings.Contains(source, ":") {
		return ""
	}
	u, err := url.Parse(source)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(u.Scheme))
}

func IsPostgresDSN(source string) bool {
	switch sourceScheme(strings.TrimSpace(source)) {
	case "postgres", "postgresql":
		return true
	default:
		return false
	}
}

func normalizeDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", DriverSQLite:
		return DriverSQLite
	case DriverPGX, "postgres", "postgresql":
		return DriverPGX
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}

func OpenSQL(cfg Config) (*sql.DB, error) {
	if cfg.Driver != DriverPGX {
		return nil, fmt.Errorf("database/sql open only supports %s, got %s", DriverPGX, cfg.Driver)
	}
	db, err := sql.Open(DriverPGX, cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("open pgx database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping pgx database: %w", err)
	}
	return db, nil
}

func DisplaySource(cfg Config) string {
	if cfg.Driver != DriverPGX {
		return cfg.Source
	}
	u, err := url.Parse(cfg.Source)
	if err != nil || u.User == nil {
		return cfg.Source
	}
	if username := u.User.Username(); username != "" {
		u.User = url.UserPassword(username, "xxxxx")
	}
	return u.String()
}

func RebindPostgres(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	arg := 1
	for _, r := range query {
		if r == '?' {
			b.WriteByte('$')
			b.WriteString(fmt.Sprintf("%d", arg))
			arg++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func Placeholders(driver string, count int) string {
	if count <= 0 {
		return ""
	}
	values := make([]string, count)
	for i := 0; i < count; i++ {
		if driver == DriverPGX {
			values[i] = fmt.Sprintf("$%d", i+1)
		} else {
			values[i] = "?"
		}
	}
	return strings.Join(values, ",")
}

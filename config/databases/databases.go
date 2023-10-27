package databases

import (
	"fmt"

	"github.com/bitcoin-sv/pulse/config"
	"github.com/jmoiron/sqlx"
)

type dbSetupFunc func(c *config.Db) (*sqlx.DB, error)
type dbSetups map[config.DbType]dbSetupFunc

// NewDbSetup will load the db setup functions into a lookup map
// ready for being called in main.go.
func NewDbSetup() dbSetups {
	s := make(map[config.DbType]dbSetupFunc, 3)
	s[config.DBSqlite] = setupSqliteDB
	return s
}

func (d dbSetups) SetupDb(cfg *config.Db) (*sqlx.DB, error) {
	fn, ok := d[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("db type %s not supported", cfg.Type)
	}
	return fn(cfg)
}

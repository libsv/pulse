package database

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bitcoin-sv/pulse/config"
	"github.com/bitcoin-sv/pulse/database/sql"
	"github.com/bitcoin-sv/pulse/internal/chaincfg/chainhash"
	"github.com/bitcoin-sv/pulse/repository/dto"
	"github.com/golang-migrate/migrate/v4"
	sqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/rs/zerolog"

	// use blank import to use file source driver with the migrate package.
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"

	// use blank import to register sqlite driver.
	_ "github.com/mattn/go-sqlite3"
)

type sqLiteAdapter struct {
	db *sqlx.DB
}

type sqLitePragmaValues struct {
	Synchronous int
	JournalMode string
	CacheSize   int
}

const sqliteDriverName = "sqlite3"
const sqliteBatchSize = 500

func (a *sqLiteAdapter) connect(cfg *config.DbConfig) error {
	dsn := fmt.Sprintf("file:%s?_foreign_keys=true&pooling=true", cfg.Sqlite.FilePath)
	db, err := sqlx.Open(sqliteDriverName, dsn)
	if err != nil {
		return err
	}

	a.db = db
	return nil
}

func (a *sqLiteAdapter) doMigrations(cfg *config.DbConfig) error {
	driver, err := sqlite3.WithInstance(a.db.DB, &sqlite3.Config{})
	if err != nil {
		return err
	}

	sourceUrl := fmt.Sprintf("file://%s", cfg.SchemaPath)
	driverName := sqliteDriverName

	m, err := migrate.NewWithDatabaseInstance(sourceUrl, driverName, driver)
	if err != nil {
		return err
	}

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return err
	}

	return nil
}

func (a *sqLiteAdapter) getDBx() *sqlx.DB {
	if a.db == nil {
		panic("connection to the database has not been established")
	}
	return a.db
}

func (a *sqLiteAdapter) importHeaders(inputFile *os.File, log *zerolog.Logger) (int, error) {
	// prepare db to bulk insterts
	restorePragmas, err := modifySqLitePragmas(a.db)
	if err != nil {
		return 0, err
	}

	defer func() {
		if err = restorePragmas(); err != nil {
			log.Error().Msg(err.Error())
		}
	}()

	restoreIndexes, err := a.dropTableIndexes(sql.HeadersTableName)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err = restoreIndexes(); err != nil {
			log.Error().Msg(err.Error())
		}
	}()

	// Read from the beginning of the file
	if _, err := inputFile.Seek(0, 0); err != nil {
		return 0, err
	}

	reader := csv.NewReader(inputFile)
	_, err = reader.Read() // Skipping the column headers line
	if err != nil {
		return 0, err
	}

	repo := sql.NewHeadersDb(a.db, log)

	previousBlockHash := chainhash.Hash{}.String()
	rowIndex := 0
	guard := 0

	for {
		rowIndex, err = a.insertHeaders(reader, repo, sqliteBatchSize, previousBlockHash, rowIndex)
		if err != nil {
			return 0, err
		}

		if guard == rowIndex {
			break
		}

		guard = rowIndex
	}

	return rowIndex, nil
}

func modifySqLitePragmas(db *sqlx.DB) (func() error, error) {
	old_pragmas, err := getSqLitePragmaValues(db)
	if err != nil {
		return nil, err
	}

	pragmas := []string{
		"PRAGMA synchronous = OFF;",
		"PRAGMA journal_mode = MEMORY;",
		"PRAGMA cache_size = 10000;",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			restoreSqLitePragmas(db, *old_pragmas)
			return nil, err
		}
	}

	return func() error { return restoreSqLitePragmas(db, *old_pragmas) }, nil
}

func getSqLitePragmaValues(db *sqlx.DB) (*sqLitePragmaValues, error) {
	var pragmaValues sqLitePragmaValues

	pragmaQueries := map[string]interface{}{
		"synchronous":  &pragmaValues.Synchronous,
		"journal_mode": &pragmaValues.JournalMode,
		"cache_size":   &pragmaValues.CacheSize,
	}

	for pragmaName, target := range pragmaQueries {
		query := fmt.Sprintf("PRAGMA %s", pragmaName)
		err := db.QueryRow(query).Scan(target)
		if err != nil {
			return nil, err
		}
	}

	return &pragmaValues, nil
}

func restoreSqLitePragmas(db *sqlx.DB, values sqLitePragmaValues) error {
	pragmas := []string{
		fmt.Sprintf("PRAGMA synchronous = %d;", values.Synchronous),
		fmt.Sprintf("PRAGMA journal_mode = %s;", values.JournalMode),
		fmt.Sprintf("PRAGMA cache_size = %d;", values.CacheSize),
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return err
		}
	}

	return nil
}

// dropTableIndexes removes indexes from a table. Returns the index restore function if successful.
func (a *sqLiteAdapter) dropTableIndexes(table string) (func() error, error) {
	q := fmt.Sprintf("SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name ='%s' AND sql IS NOT NULL;", table)
	return dropIndexes(a.db, &q)
}

func (a *sqLiteAdapter) insertHeaders(reader *csv.Reader, repo *sql.HeadersDb, batchSize int, previousBlockHash string, rowIndex int) (lastRowIndex int, err error) {
	lastRowIndex = rowIndex
	batch := make([]dto.DbBlockHeader, 0, batchSize)

	for i := 0; i < batchSize; i++ {
		record, readErr := reader.Read()
		if err != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			err = fmt.Errorf("error reading record: %v", readErr)
			return
		}

		if len(record) == 0 {
			break
		}

		block := parseRecord(record, int32(lastRowIndex), previousBlockHash)
		batch = append(batch, block)

		previousBlockHash = block.Hash
		lastRowIndex++
	}

	if err = repo.CreateMultiple(context.Background(), batch); err != nil {
		return
	}

	return
}

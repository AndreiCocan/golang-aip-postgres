// Package pgtest runs the execution tests of a package against a real
// PostgreSQL server, which it starts in Docker with testcontainers-go.
//
// A test package calls [Main] from its TestMain, and each execution test
// calls [DB]. In short mode (go test -short) no server starts, and [DB]
// skips the test, so the unit tests run without Docker.
package pgtest

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"testing"

	// The pgx driver registers itself as "pgx" for database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// image is the PostgreSQL image of the server.
const image = "postgres:18-alpine"

// db is the database of the server, or nil in short mode. TestMain has no
// way to hand values to the tests, so it is a global.
//
//nolint:gochecknoglobals // Set once by Main before the tests run.
var db *sql.DB

// Main starts the server, runs the tests of m, stops the server, and exits
// with the result of the tests. In short mode it only runs the tests. It
// exits with status 1 when the server does not start.
func Main(m *testing.M) {
	flag.Parse()

	if testing.Short() {
		os.Exit(m.Run())
	}

	os.Exit(run(m))
}

// run starts the server, runs the tests of m, stops the server, and
// returns the exit code.
func run(m *testing.M) int {
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("test"),
		postgres.BasicWaitStrategies(),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			fmt.Fprintf(os.Stderr, "pgtest: stop postgres: %v\n", err)
		}
	}()

	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: start postgres: %v\n", err)

		return 1
	}

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: connection string: %v\n", err)

		return 1
	}

	db, err = sql.Open("pgx", url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgtest: open database: %v\n", err)

		return 1
	}

	defer func() { _ = db.Close() }()

	return m.Run()
}

// DB returns the database of the server that [Main] started. It skips tb
// in short mode. All the tests of a package share the database.
func DB(tb testing.TB) *sql.DB {
	tb.Helper()

	if db == nil {
		tb.Skip("execution test needs PostgreSQL; run without -short")
	}

	return db
}

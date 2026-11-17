// Package store is the database layer. The queries in queries/*.sql are compiled by
// sqlc (`make generate`) into the generated *.sql.go, models.go, querier.go and db.go
// files; store.go adds hand-written transaction support on top.
package store

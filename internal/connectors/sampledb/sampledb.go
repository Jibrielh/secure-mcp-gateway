package sampledb

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	_ "modernc.org/sqlite"
)

const seed = `
CREATE TABLE IF NOT EXISTS regions (name TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS products (name TEXT PRIMARY KEY, region TEXT NOT NULL, revenue_cents INTEGER NOT NULL);
INSERT INTO regions (name) VALUES ('North'), ('South'), ('West') ON CONFLICT DO NOTHING;
INSERT INTO products (name, region, revenue_cents) VALUES
  ('Widget', 'North', 12000),
  ('Gadget', 'North', 8000),
  ('Cable', 'South', 5000),
  ('Sensor', 'West', 15000)
ON CONFLICT DO NOTHING;
`

type DB struct{ db *sql.DB }

func Open(dsn string) (*DB, error) {
	if !strings.HasPrefix(dsn, "file:") {
		return nil, errors.New("sample database must be a sqlite file DSN")
	}
	writer, err := sql.Open("sqlite", strings.ReplaceAll(strings.ReplaceAll(dsn, "&mode=ro", ""), "?mode=ro", ""))
	if err != nil {
		return nil, err
	}
	if _, err := writer.Exec(seed); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	readOnly := dsn
	if !strings.Contains(readOnly, "mode=ro") {
		if strings.Contains(readOnly, "?") {
			readOnly += "&mode=ro"
		} else {
			readOnly += "?mode=ro"
		}
	}
	db, err := sql.Open("sqlite", readOnly)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) ListRegions(ctx context.Context) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT name FROM regions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

type Revenue struct {
	Region       string `json:"region"`
	RevenueCents int    `json:"revenue_cents"`
}

func (d *DB) RevenueByRegion(ctx context.Context, region string) ([]Revenue, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT region, SUM(revenue_cents) FROM products WHERE (? = '' OR region = ?) GROUP BY region ORDER BY region`, region, region)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Revenue
	for rows.Next() {
		var item Revenue
		if err := rows.Scan(&item.Region, &item.RevenueCents); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type Product struct {
	Name         string `json:"name"`
	Region       string `json:"region"`
	RevenueCents int    `json:"revenue_cents"`
}

func (d *DB) TopProducts(ctx context.Context, limit int) ([]Product, error) {
	if limit < 1 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	rows, err := d.db.QueryContext(ctx, `SELECT name, region, revenue_cents FROM products ORDER BY revenue_cents DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Product
	for rows.Next() {
		var item Product
		if err := rows.Scan(&item.Name, &item.Region, &item.RevenueCents); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (d *DB) WriteRejected(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO regions (name) VALUES ('East')`)
	if err == nil {
		return errors.New("read-only database accepted a write")
	}
	return nil
}

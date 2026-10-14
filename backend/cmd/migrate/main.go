// Command migrate transfers existing ListenLedger records from a source database
// (e.g. SQLite) to a target database (e.g. Supabase PostgreSQL) and reconciles
// all table counts and per-tenant inventory/revenue invariants.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/storage"
)

func main() {
	var (
		srcPath = flag.String("src", "listenledger.db", "source database path (SQLite file)")
		dstURL  = flag.String("dst", "", "destination database URL (PostgreSQL connection string or target path)")
		dryRun  = flag.Bool("dry-run", false, "perform transfer and reconciliation inside a transaction then roll back")
		jsonFmt = flag.Bool("json", false, "format reconciliation report as JSON")
	)
	flag.Parse()

	if *dstURL == "" {
		if env := os.Getenv("DATABASE_URL"); env != "" {
			*dstURL = env
		}
	}

	if *srcPath == "" || *dstURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: migrate -src <source.db> -dst <postgres_url> [-dry-run] [-json]")
		fmt.Fprintln(os.Stderr, "Error: both source and destination must be specified (or set DATABASE_URL)")
		os.Exit(1)
	}

	if strings.TrimSpace(*srcPath) == strings.TrimSpace(*dstURL) {
		fmt.Fprintln(os.Stderr, "Error: source and destination cannot be identical")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	fmt.Printf("=== ListenLedger Data Migration ===\n")
	fmt.Printf("Source:      %s\n", *srcPath)
	fmt.Printf("Destination: %s\n", redactDSN(*dstURL))
	fmt.Printf("Dry-Run:     %t\n\n", *dryRun)

	srcStore, err := storage.Open(*srcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening source store: %v\n", err)
		os.Exit(1)
	}
	defer srcStore.Close()

	dstStore, err := storage.Open(*dstURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening destination store: %v\n", err)
		os.Exit(1)
	}
	defer dstStore.Close()

	report, err := storage.Transfer(ctx, srcStore, dstStore, *dryRun)
	if *jsonFmt {
		enc, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(enc))
	} else {
		printReport(report)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\nMigration FAILED: %v\n", err)
		os.Exit(1)
	}

	if !report.AllMatched {
		fmt.Fprintf(os.Stderr, "\nMigration FAILED: reconciliation discrepancies detected.\n")
		os.Exit(1)
	}

	if *dryRun {
		fmt.Printf("\nDry-run completed successfully with 100%% reconciliation match. No destination data changed.\n")
	} else {
		fmt.Printf("\nMigration committed successfully in %v. All records transferred and verified.\n", report.Duration)
	}
}

func redactDSN(dsn string) string {
	if !strings.Contains(dsn, "@") {
		return dsn
	}
	parts := strings.SplitN(dsn, "@", 2)
	prefix := parts[0]
	if colon := strings.LastIndex(prefix, ":"); colon != -1 {
		prefix = prefix[:colon] + ":***"
	}
	return prefix + "@" + parts[1]
}

func printReport(r storage.ReconciliationReport) {
	fmt.Println("Table Record Reconciliation:")
	fmt.Printf("  %-20s | %-10s | %-10s | %-8s\n", "Table", "Source", "Target", "Status")
	fmt.Printf("  --------------------------------------------------------\n")
	for _, t := range r.Tables {
		status := "MATCH"
		if !t.Match {
			status = "MISMATCH"
		}
		fmt.Printf("  %-20s | %-10d | %-10d | %-8s\n", t.TableName, t.SrcCount, t.DstCount, status)
	}

	if len(r.Shops) > 0 {
		fmt.Println("\nShop Inventory & Revenue Invariant Reconciliation:")
		for _, s := range r.Shops {
			fmt.Printf("  Shop: %s\n", s.ShopID)
			fmt.Printf("    - Products:        src=%d, dst=%d (match: %t)\n", s.SrcProductCount, s.DstProductCount, s.ProductCountMatch)
			fmt.Printf("    - Total Stock Qty: src=%d, dst=%d (match: %t)\n", s.SrcStockSum, s.DstStockSum, s.StockSumMatch)
			fmt.Printf("    - Confirmed Sales: src=%d, dst=%d (match: %t)\n", s.SrcSalesRevenue, s.DstSalesRevenue, s.SalesRevenueMatch)
			fmt.Printf("    - Pending Reviews: src=%d, dst=%d (match: %t)\n", s.SrcPendingReviews, s.DstPendingReviews, s.PendingReviewsMatch)
		}
	}
}

package statsdb

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type VendorStat struct {
	VendorID      string
	VendorName    string
	EndpointID    string
	EndpointName  string
	Path          string
	Date          string
	InterfaceType string
	TargetHeaders string
	DurationMs    int64
	StatusCode    int
	Status        string

	InputTokens  int64
	OutputTokens int64
	CachedCreate int64
	CachedRead   int64
	Reasoning    int64
}

type VendorStatsStore interface {
	InsertVendorStat(ctx context.Context, stat VendorStat) error
	Close() error
}

type SQLiteVendorStatsStore struct {
	db *sql.DB
}

func OpenSQLiteVendorStatsStore(path string) (*SQLiteVendorStatsStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty sqlite path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create sqlite dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &SQLiteVendorStatsStore{db: db}
	if err := store.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteVendorStatsStore) initSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("nil sqlite store")
	}
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func (s *SQLiteVendorStatsStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteVendorStatsStore) InsertVendorStat(ctx context.Context, stat VendorStat) error {
	if s == nil || s.db == nil {
		return nil
	}

	normalized := normalizeVendorStat(stat)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO vendor_stats(
  vendor_id, vendor_name, endpoint_id, endpoint_name,
  path, date, interface_type, target_headers,
  duration_ms, status_code, status,
  input_tokens, output_tokens, cached_create, cached_read, reasoning
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		normalized.VendorID,
		normalized.VendorName,
		normalized.EndpointID,
		normalized.EndpointName,
		normalized.Path,
		normalized.Date,
		normalized.InterfaceType,
		normalized.TargetHeaders,
		normalized.DurationMs,
		normalized.StatusCode,
		normalized.Status,
		normalized.InputTokens,
		normalized.OutputTokens,
		normalized.CachedCreate,
		normalized.CachedRead,
		normalized.Reasoning,
	)
	if err != nil {
		return fmt.Errorf("insert vendor_stats: %w", err)
	}
	return nil
}

func normalizeVendorStat(stat VendorStat) VendorStat {
	out := stat
	out.VendorID = strings.TrimSpace(out.VendorID)
	out.VendorName = strings.TrimSpace(out.VendorName)
	out.EndpointID = strings.TrimSpace(out.EndpointID)
	out.EndpointName = strings.TrimSpace(out.EndpointName)
	out.Path = strings.TrimSpace(out.Path)
	out.Date = strings.TrimSpace(out.Date)
	out.InterfaceType = strings.TrimSpace(out.InterfaceType)
	out.TargetHeaders = strings.TrimSpace(out.TargetHeaders)
	out.Status = strings.TrimSpace(out.Status)

	if out.VendorID == "" {
		out.VendorID = "0"
	}
	if out.VendorName == "" {
		out.VendorName = "unknown"
	}
	if out.EndpointID == "" {
		out.EndpointID = "0"
	}
	if out.EndpointName == "" {
		out.EndpointName = "unknown"
	}
	if out.Path == "" {
		out.Path = "/"
	}
	if out.Date == "" {
		out.Date = time.Now().Format("2006-01-02")
	}
	if out.InterfaceType == "" {
		out.InterfaceType = "unknown"
	}
	if out.TargetHeaders == "" {
		out.TargetHeaders = "{}"
	}
	if out.Status == "" {
		out.Status = "unknown"
	}

	if !json.Valid([]byte(out.TargetHeaders)) {
		out.TargetHeaders = "{}"
	}
	return out
}

func MustJSON(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	if !json.Valid(b) {
		return "{}"
	}
	return string(b)
}

// VendorStatsSummary represents aggregated stats for a vendor
type VendorStatsSummary struct {
	VendorID     string                 `json:"vendorId"`
	VendorName   string                 `json:"vendorName"`
	InputTokens  int64                  `json:"inputTokens"`
	OutputTokens int64                  `json:"outputTokens"`
	CachedCreate int64                  `json:"cachedCreate"`
	CachedRead   int64                  `json:"cachedRead"`
	Reasoning    int64                  `json:"reasoning"`
	Total        int64                  `json:"total"`
	Endpoints    []EndpointStatsSummary `json:"endpoints"`
}

// EndpointStatsSummary represents aggregated stats for an endpoint
type EndpointStatsSummary struct {
	EndpointID   string `json:"endpointId"`
	EndpointName string `json:"endpointName"`
	VendorName   string `json:"vendorName"`
	Date         string `json:"date,omitempty"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedCreate int64  `json:"cachedCreate"`
	CachedRead   int64  `json:"cachedRead"`
	Reasoning    int64  `json:"reasoning"`
	Total        int64  `json:"total"`
	RequestCount int64  `json:"requestCount"`
}

// InterfaceTypeStatsSummary represents aggregated stats grouped by interface type
type InterfaceTypeStatsSummary struct {
	InterfaceType string                 `json:"interfaceType"`
	InputTokens   int64                  `json:"inputTokens"`
	OutputTokens  int64                  `json:"outputTokens"`
	CachedCreate  int64                  `json:"cachedCreate"`
	CachedRead    int64                  `json:"cachedRead"`
	Reasoning     int64                  `json:"reasoning"`
	Total         int64                  `json:"total"`
	RequestCount  int64                  `json:"requestCount"`
	Endpoints     []EndpointStatsSummary `json:"endpoints"`
}

// TimeRange represents a time range for querying stats
type TimeRange string

const (
	TimeRangeToday     TimeRange = "today"
	TimeRangeYesterday TimeRange = "yesterday"
	TimeRangeWeek      TimeRange = "week"
	TimeRangeMonth     TimeRange = "month"
	TimeRangeAll       TimeRange = "all"
)

// GetStatsByTimeRange returns aggregated stats grouped by vendor for the given time range
func (s *SQLiteVendorStatsStore) GetStatsByTimeRange(ctx context.Context, timeRange TimeRange) ([]VendorStatsSummary, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("nil sqlite store")
	}

	dateCondition := buildDateCondition(timeRange)

	// Query aggregated stats grouped by vendor and endpoint
	query := fmt.Sprintf(`
		SELECT 
			vendor_id, vendor_name, endpoint_id, endpoint_name,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cached_create), 0) as cached_create,
			COALESCE(SUM(cached_read), 0) as cached_read,
			COALESCE(SUM(reasoning), 0) as reasoning
		FROM vendor_stats
		WHERE %s
		GROUP BY vendor_id, vendor_name, endpoint_id, endpoint_name
		ORDER BY vendor_name, endpoint_name
	`, dateCondition)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	// Build vendor map
	vendorMap := make(map[string]*VendorStatsSummary)
	var vendorOrder []string

	for rows.Next() {
		var vendorID, vendorName, endpointID, endpointName string
		var input, output, cachedCreate, cachedRead, reasoning int64

		if err := rows.Scan(&vendorID, &vendorName, &endpointID, &endpointName, &input, &output, &cachedCreate, &cachedRead, &reasoning); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		total := input + output + cachedCreate + cachedRead + reasoning

		endpointSummary := EndpointStatsSummary{
			EndpointID:   endpointID,
			EndpointName: endpointName,
			InputTokens:  input,
			OutputTokens: output,
			CachedCreate: cachedCreate,
			CachedRead:   cachedRead,
			Reasoning:    reasoning,
			Total:        total,
		}

		if vendor, exists := vendorMap[vendorID]; exists {
			vendor.InputTokens += input
			vendor.OutputTokens += output
			vendor.CachedCreate += cachedCreate
			vendor.CachedRead += cachedRead
			vendor.Reasoning += reasoning
			vendor.Total += total
			vendor.Endpoints = append(vendor.Endpoints, endpointSummary)
		} else {
			vendorMap[vendorID] = &VendorStatsSummary{
				VendorID:     vendorID,
				VendorName:   vendorName,
				InputTokens:  input,
				OutputTokens: output,
				CachedCreate: cachedCreate,
				CachedRead:   cachedRead,
				Reasoning:    reasoning,
				Total:        total,
				Endpoints:    []EndpointStatsSummary{endpointSummary},
			}
			vendorOrder = append(vendorOrder, vendorID)
		}
	}

	// Convert to slice maintaining order
	result := make([]VendorStatsSummary, 0, len(vendorOrder))
	for _, vendorID := range vendorOrder {
		result = append(result, *vendorMap[vendorID])
	}

	return result, nil
}

// ClearStats clears all stats or stats for a specific time range
func (s *SQLiteVendorStatsStore) ClearStats(ctx context.Context, timeRange TimeRange) error {
	if s == nil || s.db == nil {
		return errors.New("nil sqlite store")
	}

	var query string
	if timeRange == TimeRangeAll {
		query = "DELETE FROM vendor_stats"
	} else {
		dateCondition := buildDateCondition(timeRange)
		query = fmt.Sprintf("DELETE FROM vendor_stats WHERE %s", dateCondition)
	}

	fmt.Printf("[ClearStats] Executing query: %s\n", query)
	result, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("clear stats: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	fmt.Printf("[ClearStats] Rows affected: %d\n", rowsAffected)
	return nil
}

// GetStatsByInterfaceType returns aggregated stats grouped by interface type for the given time range
func (s *SQLiteVendorStatsStore) GetStatsByInterfaceType(ctx context.Context, timeRange TimeRange) ([]InterfaceTypeStatsSummary, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("nil sqlite store")
	}

	dateCondition := buildDateCondition(timeRange)

	// For "all" time range, include date in grouping
	includeDate := timeRange == TimeRangeAll

	var query string
	if includeDate {
		query = fmt.Sprintf(`
			SELECT 
				interface_type, vendor_id, vendor_name, endpoint_id, endpoint_name, date,
				COALESCE(SUM(input_tokens), 0) as input_tokens,
				COALESCE(SUM(output_tokens), 0) as output_tokens,
				COALESCE(SUM(cached_create), 0) as cached_create,
				COALESCE(SUM(cached_read), 0) as cached_read,
				COALESCE(SUM(reasoning), 0) as reasoning,
				COUNT(*) as request_count
			FROM vendor_stats
			WHERE %s
			GROUP BY interface_type, vendor_id, vendor_name, endpoint_id, endpoint_name, date
			ORDER BY interface_type, date DESC, vendor_name, endpoint_name
		`, dateCondition)
	} else {
		query = fmt.Sprintf(`
			SELECT 
				interface_type, vendor_id, vendor_name, endpoint_id, endpoint_name, '' as date,
				COALESCE(SUM(input_tokens), 0) as input_tokens,
				COALESCE(SUM(output_tokens), 0) as output_tokens,
				COALESCE(SUM(cached_create), 0) as cached_create,
				COALESCE(SUM(cached_read), 0) as cached_read,
				COALESCE(SUM(reasoning), 0) as reasoning,
				COUNT(*) as request_count
			FROM vendor_stats
			WHERE %s
			GROUP BY interface_type, vendor_id, vendor_name, endpoint_id, endpoint_name
			ORDER BY interface_type, vendor_name, endpoint_name
		`, dateCondition)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	// Build interface type map
	typeMap := make(map[string]*InterfaceTypeStatsSummary)
	var typeOrder []string

	for rows.Next() {
		var interfaceType, vendorID, vendorName, endpointID, endpointName, date string
		var input, output, cachedCreate, cachedRead, reasoning, requestCount int64

		if err := rows.Scan(&interfaceType, &vendorID, &vendorName, &endpointID, &endpointName, &date, &input, &output, &cachedCreate, &cachedRead, &reasoning, &requestCount); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		total := input + output + cachedCreate + cachedRead + reasoning

		endpointSummary := EndpointStatsSummary{
			EndpointID:   endpointID,
			EndpointName: endpointName,
			VendorName:   vendorName,
			Date:         date,
			InputTokens:  input,
			OutputTokens: output,
			CachedCreate: cachedCreate,
			CachedRead:   cachedRead,
			Reasoning:    reasoning,
			Total:        total,
			RequestCount: requestCount,
		}

		if typeSummary, exists := typeMap[interfaceType]; exists {
			typeSummary.InputTokens += input
			typeSummary.OutputTokens += output
			typeSummary.CachedCreate += cachedCreate
			typeSummary.CachedRead += cachedRead
			typeSummary.Reasoning += reasoning
			typeSummary.Total += total
			typeSummary.RequestCount += requestCount
			typeSummary.Endpoints = append(typeSummary.Endpoints, endpointSummary)
		} else {
			typeMap[interfaceType] = &InterfaceTypeStatsSummary{
				InterfaceType: interfaceType,
				InputTokens:   input,
				OutputTokens:  output,
				CachedCreate:  cachedCreate,
				CachedRead:    cachedRead,
				Reasoning:     reasoning,
				Total:         total,
				RequestCount:  requestCount,
				Endpoints:     []EndpointStatsSummary{endpointSummary},
			}
			typeOrder = append(typeOrder, interfaceType)
		}
	}

	// Convert to slice maintaining order
	result := make([]InterfaceTypeStatsSummary, 0, len(typeOrder))
	for _, interfaceType := range typeOrder {
		result = append(result, *typeMap[interfaceType])
	}

	return result, nil
}

func buildDateCondition(timeRange TimeRange) string {
	now := time.Now()
	switch timeRange {
	case TimeRangeToday:
		return fmt.Sprintf("date = '%s'", now.Format("2006-01-02"))
	case TimeRangeYesterday:
		yesterday := now.AddDate(0, 0, -1)
		return fmt.Sprintf("date = '%s'", yesterday.Format("2006-01-02"))
	case TimeRangeWeek:
		weekStart := now.AddDate(0, 0, -int(now.Weekday()))
		return fmt.Sprintf("date >= '%s'", weekStart.Format("2006-01-02"))
	case TimeRangeMonth:
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return fmt.Sprintf("date >= '%s'", monthStart.Format("2006-01-02"))
	default:
		return "1=1"
	}
}

// EndpointDailyStats represents daily stats for an endpoint
type EndpointDailyStats struct {
	EndpointID   string `json:"endpointId"`
	RequestCount int64  `json:"requestCount"`
	ErrorCount   int64  `json:"errorCount"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// GetTodayStatsByEndpoints returns today's request count and error count for each endpoint
func (s *SQLiteVendorStatsStore) GetTodayStatsByEndpoints(ctx context.Context) (map[string]*EndpointDailyStats, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("nil sqlite store")
	}

	today := time.Now().Format("2006-01-02")
	query := `
		SELECT 
			endpoint_id,
			COUNT(*) as request_count,
			SUM(CASE WHEN status_code >= 400 OR status = 'error' THEN 1 ELSE 0 END) as error_count,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens
		FROM vendor_stats
		WHERE date = ?
		GROUP BY endpoint_id
	`

	rows, err := s.db.QueryContext(ctx, query, today)
	if err != nil {
		return nil, fmt.Errorf("query today stats: %w", err)
	}
	defer rows.Close()

	result := make(map[string]*EndpointDailyStats)
	for rows.Next() {
		var endpointID string
		var requestCount, errorCount, inputTokens, outputTokens int64

		if err := rows.Scan(&endpointID, &requestCount, &errorCount, &inputTokens, &outputTokens); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		result[endpointID] = &EndpointDailyStats{
			EndpointID:   endpointID,
			RequestCount: requestCount,
			ErrorCount:   errorCount,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		}
	}

	return result, nil
}

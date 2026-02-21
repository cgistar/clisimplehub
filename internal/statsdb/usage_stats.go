package statsdb

import (
	"clisimplehub/internal/sqlitequeue"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

//go:embed schema.sql
var schemaSQL string

// UsageStat 使用统计记录
type UsageStat struct {
	EndpointID    string
	EndpointName  string
	ProviderName  string
	Path          string
	Date          string
	InterfaceType string
	TargetHeaders string
	RequestBody   string
	ResponseBody  string
	DurationMs    int64
	StatusCode    int
	Status        string

	InputTokens  int64
	OutputTokens int64
	CachedCreate int64
	CachedRead   int64
	Reasoning    int64
}

// UsageStatsStore 使用统计存储接口
type UsageStatsStore interface {
	InsertUsageStat(ctx context.Context, stat UsageStat) error
	Close() error
}

// SQLiteUsageStatsStore SQLite 实现
type SQLiteUsageStatsStore struct {
	db    *sql.DB
	queue *sqlitequeue.Manager
}

// OpenSQLiteUsageStatsStore 打开 SQLite 统计存储
func OpenSQLiteUsageStatsStore(path string) (*SQLiteUsageStatsStore, error) {
	queue, err := sqlitequeue.Open(path)
	if err != nil {
		return nil, err
	}

	store := &SQLiteUsageStatsStore{
		db:    queue.DB(),
		queue: queue,
	}
	if err := store.initSchema(context.Background()); err != nil {
		_ = queue.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteUsageStatsStore) initSchema(ctx context.Context) error {
	if s == nil || s.db == nil || s.queue == nil {
		return errors.New("nil sqlite store")
	}
	if _, err := s.queue.ExecWrite(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if err := s.ensureUsageStatsColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteUsageStatsStore) ensureUsageStatsColumns(ctx context.Context) error {
	if s == nil || s.db == nil || s.queue == nil {
		return errors.New("nil sqlite store")
	}
	hasColumn := func(table, column string) (bool, error) {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			return false, err
		}
		defer rows.Close()

		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull int
			var dflt sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				return false, err
			}
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(column)) {
				return true, nil
			}
		}
		return false, rows.Err()
	}

	ok, err := hasColumn("usage_stats", "request_body")
	if err != nil {
		return fmt.Errorf("check usage_stats columns: %w", err)
	}
	if !ok {
		if _, err := s.queue.ExecWrite(ctx, "ALTER TABLE usage_stats ADD COLUMN request_body TEXT"); err != nil {
			return fmt.Errorf("add usage_stats.request_body: %w", err)
		}
	}

	ok, err = hasColumn("usage_stats", "response_body")
	if err != nil {
		return fmt.Errorf("check usage_stats columns: %w", err)
	}
	if !ok {
		if _, err := s.queue.ExecWrite(ctx, "ALTER TABLE usage_stats ADD COLUMN response_body TEXT"); err != nil {
			return fmt.Errorf("add usage_stats.response_body: %w", err)
		}
	}
	return nil
}

func (s *SQLiteUsageStatsStore) Close() error {
	if s == nil || s.queue == nil {
		return nil
	}
	return s.queue.Close()
}

// InsertUsageStat 插入使用统计记录
func (s *SQLiteUsageStatsStore) InsertUsageStat(ctx context.Context, stat UsageStat) error {
	if s == nil || s.queue == nil {
		return nil
	}

	normalized := normalizeUsageStat(stat)
	_, err := s.queue.ExecWrite(ctx, `
INSERT INTO usage_stats(
  endpoint_id, endpoint_name, provider_name,
  path, date, interface_type, target_headers,
  request_body, response_body,
  duration_ms, status_code, status,
  input_tokens, output_tokens, cached_create, cached_read, reasoning
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		normalized.EndpointID,
		normalized.EndpointName,
		normalized.ProviderName,
		normalized.Path,
		normalized.Date,
		normalized.InterfaceType,
		normalized.TargetHeaders,
		normalized.RequestBody,
		normalized.ResponseBody,
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
		return fmt.Errorf("insert usage_stats: %w", err)
	}
	return nil
}

// DeleteStatsByEndpointID 删除指定端点的统计记录
func (s *SQLiteUsageStatsStore) DeleteStatsByEndpointID(ctx context.Context, endpointID int64) error {
	if s == nil || s.queue == nil {
		return errors.New("nil sqlite store")
	}
	if endpointID <= 0 {
		return nil
	}

	_, err := s.queue.ExecWrite(ctx, `DELETE FROM usage_stats WHERE endpoint_id = ?`, strconv.FormatInt(endpointID, 10))
	if err != nil {
		return fmt.Errorf("delete usage_stats by endpoint_id=%d: %w", endpointID, err)
	}
	return nil
}

func normalizeUsageStat(stat UsageStat) UsageStat {
	out := stat
	out.EndpointID = strings.TrimSpace(out.EndpointID)
	out.EndpointName = strings.TrimSpace(out.EndpointName)
	out.ProviderName = strings.TrimSpace(out.ProviderName)
	out.Path = strings.TrimSpace(out.Path)
	out.Date = strings.TrimSpace(out.Date)
	out.InterfaceType = strings.TrimSpace(out.InterfaceType)
	out.TargetHeaders = strings.TrimSpace(out.TargetHeaders)
	out.Status = strings.TrimSpace(out.Status)

	if out.EndpointID == "" {
		out.EndpointID = "0"
	}
	if out.EndpointName == "" {
		out.EndpointName = "unknown"
	}
	if out.ProviderName == "" {
		out.ProviderName = "unknown"
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

// ProviderStatsSummary 提供商统计汇总
type ProviderStatsSummary struct {
	ProviderName string                 `json:"providerName"`
	InputTokens  int64                  `json:"inputTokens"`
	OutputTokens int64                  `json:"outputTokens"`
	CachedCreate int64                  `json:"cachedCreate"`
	CachedRead   int64                  `json:"cachedRead"`
	Reasoning    int64                  `json:"reasoning"`
	Total        int64                  `json:"total"`
	Endpoints    []EndpointStatsSummary `json:"endpoints"`
}

// EndpointStatsSummary 端点统计汇总
type EndpointStatsSummary struct {
	EndpointID   string `json:"endpointId"`
	EndpointName string `json:"endpointName"`
	ProviderName string `json:"providerName"`
	Date         string `json:"date,omitempty"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CachedCreate int64  `json:"cachedCreate"`
	CachedRead   int64  `json:"cachedRead"`
	Reasoning    int64  `json:"reasoning"`
	Total        int64  `json:"total"`
	RequestCount int64  `json:"requestCount"`
}

// InterfaceTypeStatsSummary 接口类型统计汇总
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

// TimeRange 时间范围
type TimeRange string

const (
	TimeRangeToday     TimeRange = "today"
	TimeRangeYesterday TimeRange = "yesterday"
	TimeRangeWeek      TimeRange = "week"
	TimeRangeMonth     TimeRange = "month"
	TimeRangeAll       TimeRange = "all"
)

// GetStatsByTimeRange 按时间范围获取统计（按提供商分组）
func (s *SQLiteUsageStatsStore) GetStatsByTimeRange(ctx context.Context, timeRange TimeRange) ([]ProviderStatsSummary, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("nil sqlite store")
	}

	dateCondition := buildDateCondition(timeRange)

	query := fmt.Sprintf(`
		SELECT
			provider_name, endpoint_id, endpoint_name,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cached_create), 0) as cached_create,
			COALESCE(SUM(cached_read), 0) as cached_read,
			COALESCE(SUM(reasoning), 0) as reasoning
		FROM usage_stats
		WHERE %s
		GROUP BY provider_name, endpoint_id, endpoint_name
		ORDER BY provider_name, endpoint_name
	`, dateCondition)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	providerMap := make(map[string]*ProviderStatsSummary)
	var providerOrder []string

	for rows.Next() {
		var providerName, endpointID, endpointName string
		var input, output, cachedCreate, cachedRead, reasoning int64

		if err := rows.Scan(&providerName, &endpointID, &endpointName, &input, &output, &cachedCreate, &cachedRead, &reasoning); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		total := input + output + cachedCreate + cachedRead + reasoning

		endpointSummary := EndpointStatsSummary{
			EndpointID:   endpointID,
			EndpointName: endpointName,
			ProviderName: providerName,
			InputTokens:  input,
			OutputTokens: output,
			CachedCreate: cachedCreate,
			CachedRead:   cachedRead,
			Reasoning:    reasoning,
			Total:        total,
		}

		if provider, exists := providerMap[providerName]; exists {
			provider.InputTokens += input
			provider.OutputTokens += output
			provider.CachedCreate += cachedCreate
			provider.CachedRead += cachedRead
			provider.Reasoning += reasoning
			provider.Total += total
			provider.Endpoints = append(provider.Endpoints, endpointSummary)
		} else {
			providerMap[providerName] = &ProviderStatsSummary{
				ProviderName: providerName,
				InputTokens:  input,
				OutputTokens: output,
				CachedCreate: cachedCreate,
				CachedRead:   cachedRead,
				Reasoning:    reasoning,
				Total:        total,
				Endpoints:    []EndpointStatsSummary{endpointSummary},
			}
			providerOrder = append(providerOrder, providerName)
		}
	}

	result := make([]ProviderStatsSummary, 0, len(providerOrder))
	for _, providerName := range providerOrder {
		result = append(result, *providerMap[providerName])
	}

	return result, nil
}

// ClearStats 清除统计数据
func (s *SQLiteUsageStatsStore) ClearStats(ctx context.Context, timeRange TimeRange) error {
	if s == nil || s.queue == nil {
		return errors.New("nil sqlite store")
	}

	var query string
	if timeRange == TimeRangeAll {
		query = "DELETE FROM usage_stats"
	} else {
		dateCondition := buildDateCondition(timeRange)
		query = fmt.Sprintf("DELETE FROM usage_stats WHERE %s", dateCondition)
	}

	fmt.Printf("[ClearStats] Executing query: %s\n", query)
	result, err := s.queue.ExecWrite(ctx, query)
	if err != nil {
		return fmt.Errorf("clear stats: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	fmt.Printf("[ClearStats] Rows affected: %d\n", rowsAffected)
	return nil
}

// GetStatsByInterfaceType 按接口类型获取统计
func (s *SQLiteUsageStatsStore) GetStatsByInterfaceType(ctx context.Context, timeRange TimeRange) ([]InterfaceTypeStatsSummary, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("nil sqlite store")
	}

	dateCondition := buildDateCondition(timeRange)
	includeDate := timeRange == TimeRangeAll

	var query string
	if includeDate {
		query = fmt.Sprintf(`
			SELECT
				interface_type, provider_name, endpoint_id, endpoint_name, date,
				COALESCE(SUM(input_tokens), 0) as input_tokens,
				COALESCE(SUM(output_tokens), 0) as output_tokens,
				COALESCE(SUM(cached_create), 0) as cached_create,
				COALESCE(SUM(cached_read), 0) as cached_read,
				COALESCE(SUM(reasoning), 0) as reasoning,
				COUNT(*) as request_count
			FROM usage_stats
			WHERE %s
			GROUP BY interface_type, provider_name, endpoint_id, endpoint_name, date
			ORDER BY interface_type, date DESC, provider_name, endpoint_name
		`, dateCondition)
	} else {
		query = fmt.Sprintf(`
			SELECT
				interface_type, provider_name, endpoint_id, endpoint_name, '' as date,
				COALESCE(SUM(input_tokens), 0) as input_tokens,
				COALESCE(SUM(output_tokens), 0) as output_tokens,
				COALESCE(SUM(cached_create), 0) as cached_create,
				COALESCE(SUM(cached_read), 0) as cached_read,
				COALESCE(SUM(reasoning), 0) as reasoning,
				COUNT(*) as request_count
			FROM usage_stats
			WHERE %s
			GROUP BY interface_type, provider_name, endpoint_id, endpoint_name
			ORDER BY interface_type, provider_name, endpoint_name
		`, dateCondition)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	typeMap := make(map[string]*InterfaceTypeStatsSummary)
	var typeOrder []string

	for rows.Next() {
		var interfaceType, providerName, endpointID, endpointName, date string
		var input, output, cachedCreate, cachedRead, reasoning, requestCount int64

		if err := rows.Scan(&interfaceType, &providerName, &endpointID, &endpointName, &date, &input, &output, &cachedCreate, &cachedRead, &reasoning, &requestCount); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		total := input + output + cachedCreate + cachedRead + reasoning

		endpointSummary := EndpointStatsSummary{
			EndpointID:   endpointID,
			EndpointName: endpointName,
			ProviderName: providerName,
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

// EndpointDailyStats 端点每日统计
type EndpointDailyStats struct {
	EndpointID   string `json:"endpointId"`
	RequestCount int64  `json:"requestCount"`
	ErrorCount   int64  `json:"errorCount"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// GetTodayStatsByEndpoints 获取今日各端点统计
func (s *SQLiteUsageStatsStore) GetTodayStatsByEndpoints(ctx context.Context) (map[string]*EndpointDailyStats, error) {
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
		FROM usage_stats
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

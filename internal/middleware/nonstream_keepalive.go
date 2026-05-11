package middleware

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CodexCompactKeepAliveEnvVar 控制 /responses/compact 非流式请求的下游 keep-alive 间隔（秒）。
// 未设置或 <= 0 时关闭该机制，保持现有"错误状态码透传"行为。
// 启用后：每隔 N 秒向客户端发送一个 "\n" 并 Flush，防止客户端/中间 nginx 在长耗时 compact 过程中因空闲超时断开。
// 注意：一旦 keep-alive 实际写出字节（即 ticker 第一次触发），HTTP 响应头已 commit 为 200 OK，
// 之后无法再把上游错误状态码（如 504）透传给客户端，错误体仍会作为 JSON 正文返回。
const CodexCompactKeepAliveEnvVar = "CODEX_COMPACT_KEEPALIVE_SECONDS"

// keepAliveChunk 是兼容 JSON 解析器的最小保活字节（领先空白在 JSON 语法里是合法的）。
var keepAliveChunk = []byte("\n")

// CodexCompactKeepAliveInterval 读取环境变量，返回启用的 keep-alive 间隔。
// 返回 0 表示关闭。
func CodexCompactKeepAliveInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(CodexCompactKeepAliveEnvVar))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// StartNonStreamingKeepAlive 启动一个后台 goroutine，每隔 interval 向 w 写入一个换行并 Flush。
// 调用者必须在真正写响应（调用 WriteHeader / Write）之前调用返回的 stop()。
// 当 interval <= 0 或 w 不支持 http.Flusher 时，直接返回 no-op stop 函数。
//
// 并发安全：keep-alive goroutine 与 stop() 使用同一把互斥锁保护对 w 的写操作，
// 保证 stop() 返回后不会再有 keep-alive 字节写入。
func StartNonStreamingKeepAlive(ctx context.Context, w http.ResponseWriter, interval time.Duration) func() {
	if interval <= 0 || w == nil {
		return func() {}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	var writeMu sync.Mutex

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				writeMu.Lock()
				select {
				case <-stop:
					writeMu.Unlock()
					return
				default:
				}
				if _, err := w.Write(keepAliveChunk); err != nil {
					writeMu.Unlock()
					return
				}
				flusher.Flush()
				writeMu.Unlock()
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			writeMu.Lock()
			close(stop)
			writeMu.Unlock()
		})
		<-done
	}
}

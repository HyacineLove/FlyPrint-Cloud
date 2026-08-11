package middleware

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// LoggerMiddleware 日志中间件
func LoggerMiddleware() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
			param.ClientIP,
			param.TimeStamp.Format(time.RFC1123),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	})
}

// CORSMiddleware CORS中间件
func CORSMiddleware(allowedOrigins []string) gin.HandlerFunc {
	// 构建允许的origins映射表以提高查找效率
	allowedOriginsMap := make(map[string]bool)
	for _, origin := range allowedOrigins {
		allowedOriginsMap[origin] = true
	}

	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// 验证：支持精确匹配和通配符匹配
		if origin != "" && isOriginAllowed(origin, allowedOrigins, allowedOriginsMap) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		} else if origin != "" {
			// Origin不在白名单中，拒绝请求
			c.AbortWithStatusJSON(403, gin.H{"error": "origin not allowed"})
			return
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Requested-With, X-Fly-Print-File-Token, X-Fly-Print-Node-ID, X-Fly-Print-Printer-ID")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// isOriginAllowed 检查origin是否被允许（支持通配符）
func isOriginAllowed(origin string, allowedOrigins []string, exactMap map[string]bool) bool {
	// 1. 精确匹配（最快）
	if exactMap[origin] {
		return true
	}

	// 2. 通配符匹配
	for _, pattern := range allowedOrigins {
		if matchOriginPattern(origin, pattern) {
			return true
		}
	}

	return false
}

// matchOriginPattern 匹配origin模式
// 支持格式：
//   - http://192.168.1.100 （精确匹配）
//   - http://192.168.*.* （IP段通配符）
//   - http://*.example.com （域名通配符）
func matchOriginPattern(origin, pattern string) bool {
	// 如果没有通配符，直接比较
	if !strings.Contains(pattern, "*") {
		return origin == pattern
	}

	// 检查协议是否匹配
	originProto := ""
	patternProto := ""
	if strings.HasPrefix(origin, "http://") {
		originProto = "http://"
		origin = strings.TrimPrefix(origin, "http://")
	} else if strings.HasPrefix(origin, "https://") {
		originProto = "https://"
		origin = strings.TrimPrefix(origin, "https://")
	}

	if strings.HasPrefix(pattern, "http://") {
		patternProto = "http://"
		pattern = strings.TrimPrefix(pattern, "http://")
	} else if strings.HasPrefix(pattern, "https://") {
		patternProto = "https://"
		pattern = strings.TrimPrefix(pattern, "https://")
	}

	// 协议必须匹配
	if originProto != patternProto {
		return false
	}

	// 分离host和port
	originHost, originPort := splitHostPort(origin)
	patternHost, patternPort := splitHostPort(pattern)

	// 端口必须匹配（如果指定了）
	if patternPort != "" && originPort != patternPort {
		return false
	}

	// Host通配符匹配
	return matchWildcard(originHost, patternHost)
}

// splitHostPort 分离host和port
func splitHostPort(hostPort string) (host, port string) {
	if idx := strings.LastIndex(hostPort, ":"); idx != -1 {
		return hostPort[:idx], hostPort[idx+1:]
	}
	return hostPort, ""
}

// matchWildcard 通配符匹配
func matchWildcard(str, pattern string) bool {
	// 将pattern按*分割
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return str == pattern
	}

	// 检查开头
	if !strings.HasPrefix(str, parts[0]) {
		return false
	}
	str = str[len(parts[0]):]

	// 检查中间部分
	for i := 1; i < len(parts)-1; i++ {
		if parts[i] == "" {
			continue
		}
		idx := strings.Index(str, parts[i])
		if idx == -1 {
			return false
		}
		str = str[idx+len(parts[i]):]
	}

	// 检查结尾
	lastPart := parts[len(parts)-1]
	if lastPart == "" {
		return true
	}
	return strings.HasSuffix(str, lastPart)
}

// SecurityHeadersMiddleware 安全头中间件
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Next()
	}
}

package security

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// 密码强度要求
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

// PasswordStrength 密码强度级别
type PasswordStrength int

const (
	PasswordWeak PasswordStrength = iota
	PasswordMedium
	PasswordStrong
)

// ValidatePasswordStrength 验证密码强度
// 要求：至少8个字符，包含大写字母、小写字母、数字
func ValidatePasswordStrength(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters long", MinPasswordLength)
	}

	if len(password) > MaxPasswordLength {
		return fmt.Errorf("password must not exceed %d characters", MaxPasswordLength)
	}

	var (
		hasUpper   = false
		hasLower   = false
		hasNumber  = false
		hasSpecial = false
	)

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	if !hasUpper {
		return fmt.Errorf("password must contain at least one uppercase letter")
	}

	if !hasLower {
		return fmt.Errorf("password must contain at least one lowercase letter")
	}

	if !hasNumber {
		return fmt.Errorf("password must contain at least one number")
	}

	// 特殊字符是可选的，但推荐使用
	_ = hasSpecial

	return nil
}

// CheckPasswordStrength 检查密码强度等级（不强制要求）
func CheckPasswordStrength(password string) PasswordStrength {
	if len(password) < MinPasswordLength {
		return PasswordWeak
	}

	var (
		hasUpper   = false
		hasLower   = false
		hasNumber  = false
		hasSpecial = false
	)

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	// 计分：每种字符类型1分
	score := 0
	if hasUpper {
		score++
	}
	if hasLower {
		score++
	}
	if hasNumber {
		score++
	}
	if hasSpecial {
		score++
	}

	// 长度加分
	if len(password) >= 12 {
		score++
	}

	switch {
	case score >= 4:
		return PasswordStrong
	case score >= 3:
		return PasswordMedium
	default:
		return PasswordWeak
	}
}

// ValidateEmail 验证邮箱格式
func ValidateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("email is required")
	}

	// 简单的邮箱格式验证
	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	if !emailRegex.MatchString(email) {
		return fmt.Errorf("invalid email format")
	}

	return nil
}

// NormalizeEmail returns the canonical form used by builtin account lookup.
// Email is the login identifier, so comparisons must not depend on casing or
// accidental surrounding whitespace.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// InternalUsernameForEmail keeps the legacy users.username column populated
// without making it a second login identifier. Official and admin-created
// accounts authenticate by email only.
func InternalUsernameForEmail(email string) string {
	digest := sha256.Sum256([]byte(NormalizeEmail(email)))
	return "u_" + hex.EncodeToString(digest[:])[:48]
}

// ValidateUsername 验证用户名
func ValidateUsername(username string) error {
	if len(username) < 3 {
		return fmt.Errorf("username must be at least 3 characters long")
	}

	if len(username) > 50 {
		return fmt.Errorf("username must not exceed 50 characters")
	}

	// 只允许字母、数字、下划线、连字符
	usernameRegex := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !usernameRegex.MatchString(username) {
		return fmt.Errorf("username can only contain letters, numbers, underscores and hyphens")
	}

	return nil
}

// SanitizeFilename 清理文件名（移除危险字符）
func SanitizeFilename(filename string) string {
	// 移除路径分隔符和其他危险字符
	dangerousChars := regexp.MustCompile(`[/\\<>:"|?*\x00-\x1f]`)
	safe := dangerousChars.ReplaceAllString(filename, "_")

	// 限制长度
	if len(safe) > 255 {
		safe = safe[:255]
	}

	return safe
}

// IsAllowedFileType 检查文件类型是否在白名单中
func IsAllowedFileType(contentType string, allowedTypes []string) bool {
	for _, allowed := range allowedTypes {
		if contentType == allowed {
			return true
		}
	}
	return false
}

// AllowedPrintFileTypes 允许的打印文件类型
var AllowedPrintFileTypes = []string{
	"application/pdf",
	"application/msword",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"image/jpeg",
	"image/png",
	"image/gif",
	"image/bmp",
	"image/tiff",
}

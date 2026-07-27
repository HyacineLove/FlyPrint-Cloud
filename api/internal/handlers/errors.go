package handlers

// 错误码常量定义
const (
	// 通用错误码 (1000-1999)
	ErrCodeBadRequest          = 1000
	ErrCodeUnauthorized        = 1001
	ErrCodeForbidden           = 1002
	ErrCodeNotFound            = 1003
	ErrCodeInternalServerError = 1004
	ErrCodeValidationFailed    = 1005

	// 用户相关错误码 (2000-2099)
	ErrCodeUserNotFound           = 2000
	ErrCodeUserAlreadyExists      = 2001
	ErrCodeInvalidPassword        = 2002
	ErrCodeUserCreateFailed       = 2003
	ErrCodeUserUpdateFailed       = 2004
	ErrCodeUserDeleteFailed       = 2005
	ErrCodeUserHasActivePrintJobs = 2006

	// Edge Node 相关错误码 (3000-3099)
	ErrCodeEdgeNodeNotFound     = 3000
	ErrCodeEdgeNodeDisabled     = 3001
	ErrCodeEdgeNodeCreateFailed = 3002
	ErrCodeEdgeNodeUpdateFailed = 3003
	ErrCodeEdgeNodeDeleteFailed = 3004
	ErrCodeEdgeNodeOffline      = 3005

	// 打印机相关错误码 (4000-4099)
	ErrCodePrinterNotFound     = 4000
	ErrCodePrinterDisabled     = 4001
	ErrCodePrinterCreateFailed = 4002
	ErrCodePrinterUpdateFailed = 4003
	ErrCodePrinterDeleteFailed = 4004
	ErrCodePrinterOffline      = 4005

	// 打印任务相关错误码 (5000-5099)
	ErrCodePrintJobNotFound       = 5000
	ErrCodePrintJobCreateFailed   = 5001
	ErrCodePrintJobUpdateFailed   = 5002
	ErrCodePrintJobCancelFailed   = 5003
	ErrCodePrintJobInvalidStatus  = 5004
	ErrCodePrintJobDispatchFailed = 5005

	// 文件相关错误码 (6000-6099)
	ErrCodeFileNotFound       = 6000
	ErrCodeFileUploadFailed   = 6001
	ErrCodeFileDownloadFailed = 6002
	ErrCodeFileInvalidType    = 6003
	ErrCodeFileTooLarge       = 6004
	ErrCodeFileTooManyPages   = 6005

	// OAuth2 相关错误码 (7000-7099)
	ErrCodeOAuth2InvalidGrant  = 7000
	ErrCodeOAuth2InvalidClient = 7001
	ErrCodeOAuth2InvalidToken  = 7002
)

// 错误消息映射 (中文)
var errorMessagesZH = map[int]string{
	// 通用错误
	ErrCodeBadRequest:          "请求参数无效",
	ErrCodeUnauthorized:        "未授权",
	ErrCodeForbidden:           "禁止访问",
	ErrCodeNotFound:            "资源不存在",
	ErrCodeInternalServerError: "服务器内部错误",
	ErrCodeValidationFailed:    "字段验证失败",

	// 用户相关
	ErrCodeUserNotFound:           "用户不存在",
	ErrCodeUserAlreadyExists:      "用户已存在",
	ErrCodeInvalidPassword:        "密码错误",
	ErrCodeUserCreateFailed:       "创建用户失败",
	ErrCodeUserUpdateFailed:       "更新用户失败",
	ErrCodeUserDeleteFailed:       "删除用户失败",
	ErrCodeUserHasActivePrintJobs: "用户存在打印中的任务，无法删除",

	// Edge Node 相关
	ErrCodeEdgeNodeNotFound:     "边缘节点不存在",
	ErrCodeEdgeNodeDisabled:     "边缘节点已禁用",
	ErrCodeEdgeNodeCreateFailed: "创建边缘节点失败",
	ErrCodeEdgeNodeUpdateFailed: "更新边缘节点失败",
	ErrCodeEdgeNodeDeleteFailed: "删除边缘节点失败",
	ErrCodeEdgeNodeOffline:      "边缘节点离线",

	// 打印机相关
	ErrCodePrinterNotFound:     "打印机不存在",
	ErrCodePrinterDisabled:     "打印机已禁用",
	ErrCodePrinterCreateFailed: "创建打印机失败",
	ErrCodePrinterUpdateFailed: "更新打印机失败",
	ErrCodePrinterDeleteFailed: "删除打印机失败",
	ErrCodePrinterOffline:      "打印机离线",

	// 打印任务相关
	ErrCodePrintJobNotFound:       "打印任务不存在",
	ErrCodePrintJobCreateFailed:   "创建打印任务失败",
	ErrCodePrintJobUpdateFailed:   "更新打印任务失败",
	ErrCodePrintJobCancelFailed:   "取消打印任务失败",
	ErrCodePrintJobInvalidStatus:  "打印任务状态无效",
	ErrCodePrintJobDispatchFailed: "分发打印任务失败",

	// 文件相关
	ErrCodeFileNotFound:       "文件不存在",
	ErrCodeFileUploadFailed:   "文件上传失败",
	ErrCodeFileDownloadFailed: "文件下载失败",
	ErrCodeFileInvalidType:    "不支持的文件类型",
	ErrCodeFileTooLarge:       "文件大小超过限制",
	ErrCodeFileTooManyPages:   "文档页数超过限制",

	// OAuth2 相关
	ErrCodeOAuth2InvalidGrant:  "授权无效",
	ErrCodeOAuth2InvalidClient: "客户端无效",
	ErrCodeOAuth2InvalidToken:  "令牌无效",
}

// GetErrorMessage 获取错误消息
func GetErrorMessage(code int) string {
	if msg, ok := errorMessagesZH[code]; ok {
		return msg
	}
	return errorMessagesZH[ErrCodeInternalServerError]
}

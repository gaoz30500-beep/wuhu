package MCP

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type WriteFileIn struct {
	Path    string `json:"path" jsonschema:"要写入的文件绝对路径，例如 E:\\laji\\wuhu\\hello.txt"`
	Content string `json:"content" jsonschema:"要写入的完整文本；换行直接用 \n，不需要 shell 转义"`
	Append  bool   `json:"append,omitempty" jsonschema:"可选，true 为追加写入，默认 false 覆盖写入"`
}

type ReadFileIn struct {
	Path     string `json:"path" jsonschema:"要读取的文本文件绝对路径"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"可选，从第几个字节开始读（默认 0）。上一次结果被截断时，按它提示的 offset 继续读"`
	MaxBytes int64  `json:"maxBytes,omitempty" jsonschema:"可选，本次最多返回多少字节，默认 65536（64KB），上限 524288（512KB）"`
}

// 分页常量：默认一次给 64KB，上限 512KB。
// 太小要读很多次，太大就会一次性吃掉上下文预算。
const (
	defaultReadBytes = 64 * 1024
	maxReadBytes     = 512 * 1024
)

func registerFileTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "write_file",
		Description: "把文本内容写入指定文件，自动创建父目录。内容直接作为文本写入，不经过 shell，引号、换行、特殊字符都安全；默认覆盖，append=true 追加。",
	}, runWriteFile)

	mcp.AddTool(server, &mcp.Tool{
		Name: "read_file",
		Description: "读取指定文本文件的全部内容并返回；超长内容会被截断。" +
			"支持分页：结果被截断时会提示下一次该传的 offset，长文件可分段读完。",
	}, runReadFile)
}

func runWriteFile(ctx context.Context, req *mcp.CallToolRequest, in WriteFileIn) (*mcp.CallToolResult, any, error) {
	if in.Path == "" {
		return nil, nil, fmt.Errorf("缺少文件路径 path")
	}
	if dir := filepath.Dir(in.Path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("创建目录失败: %w", err)
		}
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if in.Append {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	file, err := os.OpenFile(in.Path, flag, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("文件打开失败: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(in.Content); err != nil {
		return nil, nil, fmt.Errorf("写入失败: %w", err)
	}
	return textResult(fmt.Sprintf("已写入文件：%s（%d 字节）", in.Path, len(in.Content))), nil, nil
}

func runReadFile(ctx context.Context, req *mcp.CallToolRequest, in ReadFileIn) (*mcp.CallToolResult, any, error) {
	if in.Path == "" {
		return nil, nil, fmt.Errorf("文件路径为空 path")
	}
	info, err := os.Stat(in.Path)
	if err != nil {
		if os.IsNotExist(err) {
			// 路径确实不存在
			return nil, nil, fmt.Errorf("文件不存在: %s", in.Path)
		}
		// 其它错误：权限不足、路径非法等
		return nil, nil, fmt.Errorf("检查路径失败: %w", err)
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("路径是目录，不是文件: %s", in.Path)
	}

	total := info.Size()
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := in.MaxBytes
	if limit <= 0 {
		limit = defaultReadBytes
	}
	if limit > maxReadBytes {
		limit = maxReadBytes
	}

	file, err := os.Open(in.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("文件打开错误: %w", err)
	}
	defer file.Close()

	// 用 ReadAt 按需读，不再整份 ReadAll：
	// 大文件（几百 MB 的日志）以前会一次性读进内存，慢且危险。
	buf := make([]byte, limit)
	n, err := file.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, nil, fmt.Errorf("文件读取错误: %w", err)
	}
	buf = buf[:n]

	// 截断点可能落在一个多字节字符中间，退到最近的字符边界，避免出现半个汉字
	if offset+int64(n) < total {
		for len(buf) > 0 && !utf8.Valid(buf) {
			buf = buf[:len(buf)-1]
		}
	}

	var sb strings.Builder
	sb.Write(buf)

	next := offset + int64(len(buf))
	if next < total {
		fmt.Fprintf(&sb, "\n\n[未读完] 文件共 %d 字节，本次读到 %d 字节（offset=%d）；继续读取请传 offset=%d",
			total, next, offset, next)
	} else if offset > 0 {
		fmt.Fprintf(&sb, "\n\n[已读到文件末尾] 文件共 %d 字节", total)
	}
	// 二进制提醒：文本工具读出来是乱码，提前说清楚省得模型瞎猜
	if idx := bytes.IndexByte(buf, 0); idx >= 0 {
		sb.WriteString("\n[提示] 内容里含 NUL 字节，可能是二进制文件")
	}

	return textResult(sb.String()), nil, nil
}

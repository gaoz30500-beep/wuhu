package MCP

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)



type WriteFileIn struct {
	Path    string `json:"path" jsonschema:"要写入的文件绝对路径，例如 E:\\laji\\wuhu\\hello.txt"`
	Content string `json:"content" jsonschema:"要写入的完整文本；换行直接用 \n，不需要 shell 转义"`
	Append  bool   `json:"append,omitempty" jsonschema:"可选，true 为追加写入，默认 false 覆盖写入"`
}

type ReadFileIn struct {
	Path string `json:"path" jsonschema:"要读取的文本文件绝对路径"`
}

func registerFileTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "write_file",
		Description: "把文本内容写入指定文件，自动创建父目录。内容直接作为文本写入，不经过 shell，引号、换行、特殊字符都安全；默认覆盖，append=true 追加。",
	}, runWriteFile)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_file",
		Description: "读取指定文本文件的全部内容并返回；超长内容会被截断。",
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
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("已写入文件：%s（%d 字节）", in.Path, len(in.Content)),
		}},
	}, nil, nil
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
	file, err := os.Open(in.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("文件打开错误: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, fmt.Errorf("文件读取错误: %w", err)
	}
	result := strings.TrimSpace(string(content))

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: result,
		}},
	}, nil, nil
}

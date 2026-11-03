package MCP

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tool interface {
	GetName() string
	GetDescription() string
	GetSchema() string //schema 架构
	NewConfig() any
	Run(ctx context.Context, req *mcp.CallToolRequest, cfg any) (*mcp.CallToolResult, error)
}

// registry 花名册 存所有已经登记的工具
var registry []Tool

// Register 给工具调用，登记自己
func Register(t Tool) {
	for _, item := range registry {
		if item.GetName() == t.GetName() {
			panic("重复工具名：" + t.GetName())
		}
	}
	registry = append(registry, t)
}

//ListTools 拿出全部登记好的工具

func ListTools() []Tool {
	return registry
}

// 配置相关
var rootCfg struct {
	Tools map[string]json.RawMessage `json:"tools"`
}

// LoadRootConfig 读取json配置文件

func LoadRootConfig(path string) error {
	date, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ReadFile: %w", err)
	}
	return json.Unmarshal(date, &rootCfg)
}

func GetToolConfig(toolname string, out any) error {
	raw, ok := rootCfg.Tools[toolname]
	if !ok {
		return fmt.Errorf("找不到这个工具的配置：%s", toolname)
	}
	return json.Unmarshal(raw, out)
}

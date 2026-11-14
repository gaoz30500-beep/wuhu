package MCP

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Config 对应 mcp_config.json。
// 设计原则：任何节点、任何字段都可以缺省，缺了就回落到代码里的默认值。
// 以前的实现是"缺一个 tools 节点就让 server 起不来"，那等于一个工具的配置
// 写错就拖垮全部工具。
type Config struct {
	Tools struct {
		Scan   ScanConfig     `json:"scan_idle_file"`
		Loc    LocationConfig `json:"get_location"`
		Save   MemoryConfig   `json:"save_memory"`
		Recall MemoryConfig   `json:"recall_memory"`
	} `json:"tools"`
}

// cfg 是只读的：加载完成后没有任何 handler 会改写它，
// 所以多个 tools/call 并发执行时不存在数据竞争。
var cfg Config

// loadConfig 读取配置文件。
// 文件不存在 / JSON 坏掉都不当成致命错误：用默认值继续跑，但要把问题打到 stderr
// （stderr 不属于 JSON-RPC 通道，不会污染协议流）。
func loadConfig(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("配置文件 %s 不存在，全部使用默认值", path)
			return nil
		}
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Printf("配置文件 %s 解析失败，全部使用默认值: %v", path, err)
		return nil
	}
	return nil
}

// resolveConfigPath 配置文件的查找顺序：
//  1. 环境变量 AGENT_MCP_CONFIG（部署时最灵活）
//  2. 当前工作目录下的 MCP/mcp_config.json（go run / IDE 调试的常见情形）
//  3. 可执行文件同目录的 mcp_config.json（打包成 exe 后不会再依赖 cwd）
//
// 找不到时返回第 2 个候选，让 loadConfig 打一条"文件不存在"的提示。
func resolveConfigPath() string {
	if p := os.Getenv("AGENT_MCP_CONFIG"); p != "" {
		return p
	}

	cwdCandidate := filepath.Join("MCP", "mcp_config.json")
	if _, err := os.Stat(cwdCandidate); err == nil {
		return cwdCandidate
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "mcp_config.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return cwdCandidate
}

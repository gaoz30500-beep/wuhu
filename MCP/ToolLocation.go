package MCP

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LocationConfig 定位工具的配置（对应 mcp_config.json 里 "get_location" 节点）
type LocationConfig struct {
	ApiURL  string `json:"apiUrl,omitempty"`  // IP定位接口URL，留空用默认(ip-api.com 中文版)
	Timeout int64  `json:"timeout,omitempty"` // HTTP超时秒数，默认8
}

// LocationTool 查看当前所在位置（基于公网IP，城市级精度）
type LocationTool struct{}

func (l *LocationTool) GetName() string {
	return "get_location"
}

func (l *LocationTool) GetDescription() string {
	return "获取当前设备所在的大致位置（国家/省份/城市/经纬度/运营商），基于公网IP定位，城市级精度，无需系统权限"
}

func (l *LocationTool) GetSchema() string {
	// 本工具无需入参，空对象即可
	return `{
		"type": "object",
		"properties": {}
	}`
}

func (l *LocationTool) NewConfig() any {
	return &LocationConfig{}
}

// ipApiResp 对应 ip-api.com 的返回结构（lang=zh-CN 时 city/regionName 为中文）
type ipApiResp struct {
	Status     string  `json:"status"`
	Country    string  `json:"country"`
	RegionName string  `json:"regionName"` // 省份
	City       string  `json:"city"`
	Zip        string  `json:"zip"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	Timezone   string  `json:"timezone"`
	Isp        string  `json:"isp"`   // 运营商
	Query      string  `json:"query"` // 公网IP
}

func (l *LocationTool) Run(ctx context.Context, req *mcp.CallToolRequest, cfg any) (*mcp.CallToolResult, error) {
	conf := cfg.(*LocationConfig)
	apiURL := conf.ApiURL
	if apiURL == "" {
		apiURL = "http://ip-api.com/json/?lang=zh-CN"
	}
	timeout := conf.Timeout
	if timeout <= 0 {
		timeout = 8
	}

	httpClient := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := httpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("IP定位请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取定位响应失败: %w", err)
	}

	var data ipApiResp
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("解析定位响应失败: %w", err)
	}
	if data.Status != "success" {
		return nil, fmt.Errorf("定位接口返回异常: %s", string(body))
	}

	result := fmt.Sprintf(
		"当前大致位置（城市级精度）\n国家/地区：%s\n省份：%s\n城市：%s\n邮编：%s\n经纬度：%.4f, %.4f\n时区：%s\n运营商：%s\n公网IP：%s",
		data.Country, data.RegionName, data.City,
		data.Zip, data.Lat, data.Lon, data.Timezone, data.Isp, data.Query,
	)

	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: result},
		},
	}
	return res, nil
}

// init 自注册：框架通过 Register 收集，StartAutoServer 里自动挂载
func init() {
	Register(&LocationTool{})
}

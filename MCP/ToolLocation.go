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

// LocationConfig 对应 mcp_config.json 里 tools.get_location 节点
type LocationConfig struct {
	ApiURL  string `json:"apiUrl,omitempty"`  // IP 定位接口 URL，留空用默认
	Timeout int64  `json:"timeout,omitempty"` // HTTP 超时秒数，默认 8
}

// LocationIn 本工具没有入参，空结构体生成的 schema 就是 {"type":"object"}
type LocationIn struct{}

// registerLocationTool 注册定位工具
func registerLocationTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_location",
		Description: "获取当前设备所在的大致位置（国家/省份/城市/经纬度/运营商），" +
			"基于公网 IP 定位，城市级精度，无需系统权限。",
	}, runGetLocation)
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
	Query      string  `json:"query"` // 公网 IP
}

func runGetLocation(ctx context.Context, req *mcp.CallToolRequest, in LocationIn) (*mcp.CallToolResult, any, error) {
	conf := cfg.Tools.Loc

	apiURL := conf.ApiURL
	if apiURL == "" {
		apiURL = "http://ip-api.com/json/?lang=zh-CN"
	}
	timeout := conf.Timeout
	if timeout <= 0 {
		timeout = 8
	}

	// 用带 ctx 的请求：client 取消时（stdio 断开、调用超时）这里会一起退出
	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("构造定位请求失败: %w", err)
	}
	httpClient := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := httpClient.Do(reqHTTP)
	if err != nil {
		return nil, nil, fmt.Errorf("IP 定位请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("读取定位响应失败: %w", err)
	}

	var data ipApiResp
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, nil, fmt.Errorf("解析定位响应失败: %w", err)
	}
	if data.Status != "success" {
		return nil, nil, fmt.Errorf("定位接口返回异常: %s", string(body))
	}

	result := fmt.Sprintf(
		"当前大致位置（城市级精度）\n国家/地区：%s\n省份：%s\n城市：%s\n邮编：%s\n经纬度：%.4f, %.4f\n时区：%s\n运营商：%s\n公网IP：%s",
		data.Country, data.RegionName, data.City,
		data.Zip, data.Lat, data.Lon, data.Timezone, data.Isp, data.Query,
	)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: result}},
	}, nil, nil
}

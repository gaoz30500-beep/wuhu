package MCP

import (
	"context"
	"fmt"
	"strings"

	"agent/memory"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ManageMemoryIn manage_memory 的入参。
// 三个动作合成一个工具，而不是拆成 list/update/delete 三个：
// 工具描述本身也占上下文，能合就合。
type ManageMemoryIn struct {
	Action  string `json:"action" jsonschema:"操作类型：list 列出、update 修改、delete 删除"`
	ID      int64  `json:"id,omitempty" jsonschema:"update / delete 必填，要操作哪条记忆（编号来自 list 或 recall_memory 的 #数字）"`
	Content string `json:"content,omitempty" jsonschema:"update 时可填，新的记忆正文（不填表示正文不变）"`
	Tags    string `json:"tags,omitempty" jsonschema:"update 时可填，新的标签，逗号分隔（不填表示标签不变）"`
	Status  string `json:"status,omitempty" jsonschema:"update 时可填，active 生效 / done 已完成 / dropped 已废弃"`
	Keyword string `json:"keyword,omitempty" jsonschema:"list 时可填，按关键词过滤"`
	Limit   int    `json:"limit,omitempty" jsonschema:"list 时可填，最多列几条，默认 20"`
}

// registerMemoryAdminTool 注册记忆的"管理"工具：列出、修改、删除。
// save_memory 只能新增/合并，记错了没法改，这个工具补上这个缺口。
func registerMemoryAdminTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "manage_memory",
		Description: "管理长期记忆库：list 列出（可按关键词过滤）、update 按编号修改正文/标签/状态、delete 按编号删除。" +
			"只有 save_memory 记错了、过时了、或用户明确要求忘掉某事时才用；平时记录新信息仍用 save_memory。",
	}, runManageMemory)
}

func runManageMemory(ctx context.Context, req *mcp.CallToolRequest, in ManageMemoryIn) (*mcp.CallToolResult, any, error) {
	storePath := cfg.Tools.Save.StorePath
	action := strings.ToLower(strings.TrimSpace(in.Action))

	switch action {
	case "list", "":
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		list, err := memory.List(storePath, in.Keyword, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("读取记忆库失败: %w", err)
		}
		if len(list) == 0 {
			return textResult("记忆库还是空的。"), nil, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "共 %d 条记忆（最多列 %d 条）：\n", len(list), limit)
		for i, r := range list {
			if i >= limit {
				sb.WriteString("…（还有更多，可用 keyword 过滤或调大 limit）")
				break
			}
			fmt.Fprintf(&sb, "- #%d [%s/%s] %s", r.ID, memory.KindOf(r), r.Status, r.Content)
			if r.Tags != "" {
				fmt.Fprintf(&sb, "（%s）", r.Tags)
			}
			sb.WriteString("\n")
		}
		return textResult(strings.TrimRight(sb.String(), "\n")), nil, nil

	case "update":
		if in.ID <= 0 {
			return textResult("update 需要 id：先用 list 查到要改的是哪条"), nil, nil
		}
		status := strings.ToLower(strings.TrimSpace(in.Status))
		if status != "" && status != memory.StatusActive &&
			status != memory.StatusDone && status != memory.StatusDropped {
			return textResult(fmt.Sprintf("status 只能取 active / done / dropped，收到的是 %q", in.Status)), nil, nil
		}
		if strings.TrimSpace(in.Content) == "" && strings.TrimSpace(in.Tags) == "" && status == "" {
			return textResult("没有要改的内容：content / tags / status 至少给一个"), nil, nil
		}

		r, err := memory.Update(storePath, in.ID, func(rec *memory.Record) {
			if c := strings.TrimSpace(in.Content); c != "" {
				rec.Content = c
			}
			if t := strings.TrimSpace(in.Tags); t != "" {
				rec.Tags = t
				rec.Kind = memory.KindOf(memory.Record{Tags: t})
			}
			if status != "" {
				rec.Status = status
			}
		})
		if err != nil {
			return textResult(fmt.Sprintf("修改失败：%v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("已修改 #%d（%s，权重 %d）：%s", r.ID, r.Status, r.Weight, r.Content)), nil, nil

	case "delete":
		if in.ID <= 0 {
			return textResult("delete 需要 id：先用 list 查到要删的是哪条"), nil, nil
		}
		// 先把原文取出来，回话里带上，用户能确认删对了
		var removedText string
		if all, err := memory.List(storePath, "", 0); err == nil {
			for _, r := range all {
				if r.ID == in.ID {
					removedText = r.Content
					break
				}
			}
		}
		n, err := memory.Delete(storePath, in.ID)
		if err != nil {
			return textResult(fmt.Sprintf("删除失败：%v", err)), nil, nil
		}
		if n == 0 {
			return textResult(fmt.Sprintf("没有 ID 为 %d 的记忆，可以用 list 查一下当前有哪些", in.ID)), nil, nil
		}
		return textResult(fmt.Sprintf("已删除 #%d：%s", in.ID, removedText)), nil, nil

	default:
		return textResult(fmt.Sprintf("不认识的 action：%q，只支持 list / update / delete", in.Action)), nil, nil
	}
}

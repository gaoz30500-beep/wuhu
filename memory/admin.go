package memory

import (
	"strings"
)

// List 列出记忆，按文件顺序（写入先后）；keyword 非空时只保留包含关键词的条目。
// 修改/删除分别用 maintain.go 里的 Update / Delete，这里只补"列出来看看"这个缺口。
func List(path, keyword string, limit int) ([]Record, error) {
	list, err := LoadAll(path)
	if err != nil {
		return nil, err
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))

	out := make([]Record, 0, len(list))
	for _, r := range list {
		if kw != "" {
			text := strings.ToLower(r.Content + " " + r.Tags)
			if !strings.Contains(text, kw) {
				continue
			}
		}
		out = append(out, r)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

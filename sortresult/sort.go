package sortresult

// scanner包内代码
import (
	"agent/scanner"
	"sort"
)

func SortByAtimeAsc(list []scanner.Resource) {
	sort.Slice(list, func(i, j int) bool {
		return list[i].Atime.Before(list[j].Atime)
	})
}

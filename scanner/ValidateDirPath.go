package scanner

import (
	"fmt"
	"os"
)

// ValidateDirPath 校验路径是否存在并且是目录
func ValidateDirPath(path string) error {
	statInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("路径不存在或无法访问：%w", err)
	}
	if !statInfo.IsDir() {
		return fmt.Errorf("目标路径不是文件夹")
	}
	return nil
}

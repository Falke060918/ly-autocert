package agent

import (
	"errors"
	"os"
	"syscall"
)

func preserveOwner(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("파일 소유자를 확인할 수 없습니다")
	}
	return os.Chown(destination, int(stat.Uid), int(stat.Gid))
}

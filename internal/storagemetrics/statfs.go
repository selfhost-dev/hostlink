package storagemetrics

import "syscall"

type statfsProvider struct{}

func NewStatfsProvider() StatfsProvider {
	return &statfsProvider{}
}

func (p *statfsProvider) Statfs(path string) (StatfsResult, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return StatfsResult{}, err
	}
	return StatfsResult{
		Bsize:  int64(stat.Bsize),
		Blocks: stat.Blocks,
		Bfree:  stat.Bfree,
		Bavail: stat.Bavail,
		Files:  stat.Files,
		Ffree:  stat.Ffree,
	}, nil
}

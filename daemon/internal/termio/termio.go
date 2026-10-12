package termio

import "os"

// IsTerminal 은 w 가 문자 장치(터미널)인지 본다.
func IsTerminal(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

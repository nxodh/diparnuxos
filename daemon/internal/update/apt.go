package update

import (
	"regexp"
	"sort"
	"strings"
)

// simLine 은 apt-get -s 가 출력하는 Inst 줄이다.
//
//	Inst linux-image-amd64 [6.12.111-1] (6.12.112-1 Debian-Security:13/stable-security [amd64])
//	Inst linux-image-6.12.112+deb13-amd64 (6.12.112-1 Debian-Security:13/stable-security [amd64])
var (
	instRE = regexp.MustCompile(`^Inst (\S+)(?: \[([^\]]+)\])? \((\S+)[ )]`)
	remvRE = regexp.MustCompile(`^Remv (\S+)`)
	// kernelImageRE 는 실제 커널 이미지 패키지 이름이다. 메타 패키지(linux-image-amd64)와 -dbg, -unsigned 는 뺀다.
	kernelImageRE = regexp.MustCompile(`^linux-image-([0-9]+\.[0-9]+[^ ]*-amd64)$`)
)

// parseSimulation 은 apt-get -s 의 출력을 변경 목록과 제거 목록으로 바꾼다.
func parseSimulation(out string) (changes []Change, removals []string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r ")
		if m := instRE.FindStringSubmatch(line); m != nil {
			changes = append(changes, Change{Name: m[1], From: m[2], To: m[3], New: m[2] == ""})
			continue
		}
		if m := remvRE.FindStringSubmatch(line); m != nil {
			removals = append(removals, m[1])
		}
	}
	return changes, removals
}

// kernelRelease 는 패키지 이름이 커널 이미지면 릴리스를, 아니면 빈 문자열을 돌려준다.
func kernelRelease(pkg string) string {
	if strings.HasSuffix(pkg, "-unsigned") || strings.HasSuffix(pkg, "-dbg") {
		return ""
	}
	if m := kernelImageRE.FindStringSubmatch(pkg); m != nil {
		return m[1]
	}
	return ""
}

// isKernelVersioned 는 특정 커널 버전에 묶인 패키지(이미지, 헤더, kbuild)인지 본다. 메타 패키지는 아니다.
func isKernelVersioned(pkg string) bool {
	for _, p := range []string{"linux-image-", "linux-headers-", "linux-kbuild-"} {
		if strings.HasPrefix(pkg, p) {
			rest := strings.TrimPrefix(pkg, p)
			return rest != "" && rest[0] >= '0' && rest[0] <= '9'
		}
	}
	return false
}

// managedSources 는 드라이버 계열 소스 패키지다. 이미지 빌드 훅의 hold 대상과 같다.
var managedSources = map[string]bool{
	"nvidia-graphics-drivers": true,
	"nvidia-kmod-open":        true,
	"nvidia-modprobe":         true,
	"nvidia-persistenced":     true,
}

// managedPackage 는 갱신 도구가 hold 를 풀고 다시 거는 패키지인지 판정한다.
// held 에 이미 있으면 관리 대상이다. 그렇지 않으면 커널 패키지와 드라이버 소스 패키지다.
func managedPackage(name, source string, held map[string]bool) bool {
	if held[name] {
		return true
	}
	if strings.HasPrefix(name, "linux-image-") || strings.HasPrefix(name, "linux-headers-") || strings.HasPrefix(name, "linux-kbuild-") {
		return true
	}
	return managedSources[source]
}

// upstreamVersion 은 Debian 버전에서 에폭과 데비안 리비전을 뺀 업스트림 버전을 돌려준다. 595.91.07-1 은 595.91.07 이다.
func upstreamVersion(v string) string {
	if i := strings.Index(v, ":"); i >= 0 {
		v = v[i+1:]
	}
	if i := strings.LastIndex(v, "-"); i >= 0 {
		v = v[:i]
	}
	return v
}

// compareVersions 는 dpkg 의 버전 비교 규칙을 따른다. a < b 이면 음수, 같으면 0, 크면 양수다.
func compareVersions(a, b string) int {
	ea, ra := splitEpoch(a)
	eb, rb := splitEpoch(b)
	if ea != eb {
		if ea < eb {
			return -1
		}
		return 1
	}
	ua, da := splitRevision(ra)
	ub, db := splitRevision(rb)
	if c := compareFragment(ua, ub); c != 0 {
		return c
	}
	return compareFragment(da, db)
}

func splitEpoch(v string) (int, string) {
	i := strings.Index(v, ":")
	if i < 0 {
		return 0, v
	}
	n := 0
	for _, c := range v[:i] {
		if c < '0' || c > '9' {
			return 0, v
		}
		n = n*10 + int(c-'0')
	}
	return n, v[i+1:]
}

func splitRevision(v string) (upstream, revision string) {
	i := strings.LastIndex(v, "-")
	if i < 0 {
		return v, ""
	}
	return v[:i], v[i+1:]
}

// order 는 dpkg 가 문자에 매기는 순서다. 틸데는 어떤 것보다 앞이고 문자는 숫자가 아닌 기호보다 앞이다.
func order(c byte) int {
	switch {
	case c == '~':
		return -1
	case c >= '0' && c <= '9':
		return 0
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		return int(c)
	default:
		return int(c) + 256
	}
}

func compareFragment(a, b string) int {
	for a != "" || b != "" {
		firstDiff := 0
		for (a != "" && !isDigit(a[0])) || (b != "" && !isDigit(b[0])) {
			var ac, bc int
			if a != "" && !isDigit(a[0]) {
				ac = order(a[0])
			}
			if b != "" && !isDigit(b[0]) {
				bc = order(b[0])
			}
			if ac != bc {
				if ac < bc {
					return -1
				}
				return 1
			}
			if a != "" {
				a = a[1:]
			}
			if b != "" {
				b = b[1:]
			}
		}
		a = strings.TrimLeft(a, "0")
		b = strings.TrimLeft(b, "0")
		for a != "" && isDigit(a[0]) && b != "" && isDigit(b[0]) {
			if firstDiff == 0 {
				firstDiff = int(a[0]) - int(b[0])
			}
			a, b = a[1:], b[1:]
		}
		if a != "" && isDigit(a[0]) {
			return 1
		}
		if b != "" && isDigit(b[0]) {
			return -1
		}
		if firstDiff != 0 {
			if firstDiff < 0 {
				return -1
			}
			return 1
		}
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// sortByVersion 은 (이름, 버전) 목록을 버전 오름차순으로 정렬한다.
func sortByVersion(list []PkgVersion) {
	sort.SliceStable(list, func(i, j int) bool { return compareVersions(list[i].Version, list[j].Version) < 0 })
}

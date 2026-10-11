// Package dist 는 systemd 유닛, sysusers, tmpfiles 설정과 기본 nftables 규칙의 원본을 둔다.
//
// 이 패키지에는 실행 코드가 없다. 테스트는 설정 파일의 내용을 읽어 정해진 값과 맞는지 확인하고,
// 기본 규칙 파일이 firewall.Ruleset 의 출력과 바이트 단위로 같은지 확인한다. 설치 경로와 역할은 README.md 에 있다.
package dist

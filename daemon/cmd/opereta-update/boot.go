package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/nxodh/operetaos/daemon/internal/boot"
	"github.com/nxodh/operetaos/daemon/internal/runner"
)

// runBoot 는 부팅 항목을 읽고 바꾸는 `opereta-update boot ...` 다.
//
//	boot status
//	boot next <opereta|windows|usb|NNNN>   다음 한 번만 그 항목으로 부팅한다(BootNext)
//	boot order <target>                    그 항목을 BootOrder 맨 앞에 둔다(펌웨어가 되돌릴 수 있다)
//	boot clear-next
//	boot repair                            GRUB 을 다시 설치하고 항목을 만든다
//	boot cleanup                           이전 이름의 ESP 폴더와 NVRAM 항목을 지운다
//	boot auto on|off                       종료 때 다음 부팅을 Opereta 로 지정하는 유닛
//
// --if-present 는 대상 항목이 없어도 실패로 보지 않는다(종료 유닛이 쓴다). --json 은 마지막 줄을 JSON 으로 낸다.
func runBoot(args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: opereta-update boot status | next <target> | order <target> | clear-next | repair | cleanup | auto on|off [--json] [--if-present]")
		return 2
	}
	var asJSON, ifPresent bool
	var pos []string
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "--if-present":
			ifPresent = true
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 {
		return usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	c := &boot.Controller{
		Runner:   runner.Exec{},
		EFIDir:   os.Getenv("OPERETA_BOOT_EFI_DIR"), // 시험용 환경 변수다. 실제 시스템에서는 설정하지 않는다.
		ESPDir:   os.Getenv("OPERETA_BOOT_ESP_DIR"),
		StateDir: os.Getenv("OPERETA_BOOT_STATE_DIR"),
	}

	var err error
	switch pos[0] {
	case "status":
		if len(pos) != 1 {
			return usage()
		}
	case "next", "order":
		if len(pos) != 2 || !boot.ValidTarget(pos[1]) {
			return usage()
		}
		if pos[0] == "next" {
			err = c.Next(ctx, pos[1])
		} else {
			err = c.Order(ctx, pos[1])
		}
	case "clear-next":
		err = c.ClearNext(ctx)
	case "repair":
		err = c.Repair(ctx)
	case "cleanup":
		err = c.Cleanup(ctx)
	case "auto":
		if len(pos) != 2 || (pos[1] != "on" && pos[1] != "off") {
			return usage()
		}
		err = c.SetAuto(ctx, pos[1] == "on")
	default:
		return usage()
	}

	var be *boot.Error
	failed := err != nil
	if failed {
		errors.As(err, &be)
		if ifPresent && be != nil && (be.Code == boot.CodeNoEntry || be.Code == boot.CodeNotUEFI) {
			failed = false
		}
	}
	info := c.Status(ctx)
	if asJSON {
		res := struct {
			OK    bool        `json:"ok"`
			Boot  *boot.Info  `json:"boot,omitempty"`
			Error *boot.Error `json:"error,omitempty"`
		}{OK: !failed, Boot: &info, Error: be}
		b, _ := json.Marshal(res)
		fmt.Fprintf(stdout, "%s\n", b)
	} else {
		if err != nil {
			fmt.Fprintf(stdout, "boot %s failed: %v\n", pos[0], err)
		}
		printBoot(stdout, info)
	}
	if failed {
		return 1
	}
	return 0
}

func printBoot(w io.Writer, in boot.Info) {
	if !in.UEFI {
		fmt.Fprintln(w, "boot mode: legacy BIOS (boot entries are not available)")
		return
	}
	fmt.Fprintf(w, "boot mode: UEFI, Secure Boot %s\n", in.SecureBoot)
	fmt.Fprintf(w, "current: %s, next: %s\n", orNone(in.Current), orNone(in.Next))
	fmt.Fprintf(w, "order: %s\n", orNone(strings.Join(in.Order, ", ")))
	for _, e := range in.Entries {
		mark := " "
		if e.Num == in.Current {
			mark = "*"
		}
		fmt.Fprintf(w, " %s %s %-10s %s\n", mark, e.Num, e.Kind, e.Label)
	}
	for _, p := range in.Problems {
		fmt.Fprintf(w, "problem: %s: %s\n", p.Code, p.Message)
	}
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

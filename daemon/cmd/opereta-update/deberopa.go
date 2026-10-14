package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nxodh/operetaos/daemon/internal/agentupdate"
)

// runDeberopa 는 `opereta-update deberopa check|apply [--auto] [--json]` 와 `deberopa auto on|off` 다.
//
// --auto 는 타이머가 부른 것이다. 네트워크가 없거나 GitHub 가 응답하지 않아도 조용히 성공(0)으로 끝내고 결과만
// 상태 파일에 남긴다. 자동 갱신이 꺼져 있으면 아무것도 하지 않는다.
func runDeberopa(args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: opereta-update deberopa check|apply [--auto] [--json] | auto on|off [--json]")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	sub, rest := args[0], args[1:]
	var asJSON, auto bool
	var pos []string
	for _, a := range rest {
		switch a {
		case "--json":
			asJSON = true
		case "--auto":
			auto = true
		default:
			pos = append(pos, a)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	// 시험용 환경 변수다. 실제 시스템에서는 설정하지 않는다.
	u := &agentupdate.Updater{Dir: os.Getenv("OPERETA_DEBEROPA_DIR"), Store: os.Getenv("OPERETA_DEBEROPA_STORE")}

	var info agentupdate.Info
	var err error
	switch {
	case (sub == "check" || sub == "apply") && len(pos) == 0:
		info, err = u.Run(ctx, agentupdate.Options{Apply: sub == "apply", Auto: auto})
	case sub == "auto" && len(pos) == 1 && (pos[0] == "on" || pos[0] == "off"):
		err = u.SetAuto(pos[0] == "on")
		info = u.ReadInfo()
	default:
		return usage()
	}

	var ae *agentupdate.Error
	failed := err != nil
	// 자동 실행에서 네트워크 문제는 실패로 치지 않는다(다음 번에 다시 시도한다).
	if auto && errors.As(err, &ae) && (ae.Code == agentupdate.CodeNetwork || ae.Code == agentupdate.CodeBusy) {
		failed = false
	}
	if asJSON {
		res := struct {
			OK    bool               `json:"ok"`
			Agent *agentupdate.Info  `json:"agent,omitempty"`
			Error *agentupdate.Error `json:"error,omitempty"`
		}{OK: !failed, Agent: &info, Error: ae}
		b, _ := json.Marshal(res)
		fmt.Fprintf(stdout, "%s\n", b)
	} else {
		printAgent(stdout, info, err)
	}
	if failed {
		return 1
	}
	return 0
}

func printAgent(w io.Writer, in agentupdate.Info, err error) {
	if err != nil {
		fmt.Fprintf(w, "deberopa update failed: %v\n", err)
	}
	fmt.Fprintf(w, "status: %s\n", in.Status)
	if in.Installed != "" {
		fmt.Fprintf(w, "installed: %s\n", in.Installed)
	}
	if in.Packaged != "" && in.Override {
		fmt.Fprintf(w, "image version: %s\n", in.Packaged)
	}
	if in.Latest != "" {
		fmt.Fprintf(w, "latest: %s\n", in.Latest)
	}
	fmt.Fprintf(w, "automatic updates: %v\n", in.Auto)
}

package supervisor

import (
	"reflect"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/llamaargs"
)

// 실행 중인 서버를 데몬이 다시 맡을 때 환경 변수에서 계획을 되살린다. 슬롯 수도 되살아야 같은 환경으로 인정한다.
func TestRuntimePlanRestoresParallelSlots(t *testing.T) {
	plan := autoconf.Output{
		NGL: "all", Ctx: 8192, SlotCtx: 2048, Parallel: 4, CacheTypeK: "q8_0", CacheTypeV: "q8_0", FlashAttn: true,
		Batch: 512, VisibleDevices: []int{0}, LoadMode: "mmap",
	}
	settings := llamaargs.ServerSettings{ModelPath: "/var/lib/diparnux/models/m.gguf", Host: "127.0.0.1", Port: 18080}
	env, err := llamaargs.Env(plan, settings)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := runtimePlan(env)
	if err != nil {
		t.Fatal(err)
	}
	if got.Parallel != 4 || got.SlotCtx != 2048 || got.Ctx != 8192 {
		t.Fatalf("restored plan = %+v", got)
	}
	again, err := llamaargs.Env(got, settings)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, env) {
		t.Fatalf("environment changed after a round trip\n got: %v\nwant: %v", again, env)
	}

	// 슬롯 수가 컨텍스트를 나누지 못하면 거부한다.
	env["LLAMA_ARG_N_PARALLEL"] = "3"
	if _, _, err := runtimePlan(env); err == nil {
		t.Fatal("a slot count that does not divide the context was accepted")
	}
	// 슬롯 수가 없으면 하나다.
	delete(env, "LLAMA_ARG_N_PARALLEL")
	one, _, err := runtimePlan(env)
	if err != nil || one.Parallel != 1 || one.SlotCtx != 8192 {
		t.Fatalf("single slot plan = %+v, %v", one, err)
	}
}

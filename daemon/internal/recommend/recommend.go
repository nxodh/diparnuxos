// Package recommend는 하드웨어에 맞는 모델을 고르는 정적 목록을 가진다.
//
// 목록은 2026-10-12에 Hugging Face API로 저장소, 파일 이름, 크기, 라이선스 태그를 확인한 값이다.
// 네트워크를 쓰지 않으며, 맞는지 보는 계산은 모델 메타데이터 없이 하는 어림 값이다.
// 실제 계획(레이어 수, 컨텍스트)은 다운로드 뒤 autoconf가 다시 계산한다.
package recommend

import "sort"

// Model은 추천 목록의 모델 하나다. 파일은 단일 GGUF 파일이다(분할 파일은 넣지 않는다).
type Model struct {
	Name    string `json:"name"`
	Repo    string `json:"repo"`
	File    string `json:"file"`
	Params  string `json:"params"`
	Quant   string `json:"quant"`
	SizeMiB int64  `json:"size_mib"`
	License string `json:"license"`
	// Tools 는 모델 카드 기준으로 채팅 틀이 도구 호출을 지원하는지다. 이 컴퓨터에서의 실제 동작은 `diparnux serve toolcheck` 로 확인한다.
	Tools bool `json:"tools"`
	// Agent 는 코딩 에이전트에 권하는지다. 도구 호출을 지원하고 8B 이상이어야 한다. 더 작은 모델은 도구를 불러도 파일을 안정적으로 고치지 못한다.
	Agent bool `json:"agent"`
}

// Catalog는 작은 모델부터 큰 모델 순으로 정렬한 목록이다.
var Catalog = []Model{
	{Name: "Qwen2.5 0.5B Instruct", Repo: "Qwen/Qwen2.5-0.5B-Instruct-GGUF", File: "qwen2.5-0.5b-instruct-q4_k_m.gguf", Params: "0.5B", Quant: "Q4_K_M", SizeMiB: 469, License: "apache-2.0", Tools: true, Agent: false},
	{Name: "Qwen3 1.7B", Repo: "ggml-org/Qwen3-1.7B-GGUF", File: "Qwen3-1.7B-Q4_K_M.gguf", Params: "1.7B", Quant: "Q4_K_M", SizeMiB: 1223, License: "apache-2.0", Tools: true, Agent: false},
	{Name: "Gemma 3 4B IT", Repo: "ggml-org/gemma-3-4b-it-GGUF", File: "gemma-3-4b-it-Q4_K_M.gguf", Params: "4B", Quant: "Q4_K_M", SizeMiB: 2374, License: "gemma", Tools: false, Agent: false},
	{Name: "Llama 3.1 8B Instruct", Repo: "bartowski/Meta-Llama-3.1-8B-Instruct-GGUF", File: "Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf", Params: "8B", Quant: "Q4_K_M", SizeMiB: 4693, License: "llama3.1", Tools: true, Agent: true},
	{Name: "Gemma 3 12B IT", Repo: "ggml-org/gemma-3-12b-it-GGUF", File: "gemma-3-12b-it-Q4_K_M.gguf", Params: "12B", Quant: "Q4_K_M", SizeMiB: 6962, License: "gemma", Tools: false, Agent: false},
	{Name: "Qwen3 14B", Repo: "ggml-org/Qwen3-14B-GGUF", File: "Qwen3-14B-Q4_K_M.gguf", Params: "14B", Quant: "Q4_K_M", SizeMiB: 8585, License: "apache-2.0", Tools: true, Agent: true},
	{Name: "Gemma 3 27B IT", Repo: "ggml-org/gemma-3-27b-it-GGUF", File: "gemma-3-27b-it-Q4_K_M.gguf", Params: "27B", Quant: "Q4_K_M", SizeMiB: 15780, License: "gemma", Tools: false, Agent: false},
	{Name: "Qwen3 32B", Repo: "ggml-org/Qwen3-32B-GGUF", File: "Qwen3-32B-Q4_K_M.gguf", Params: "32B", Quant: "Q4_K_M", SizeMiB: 18847, License: "apache-2.0", Tools: true, Agent: true},
}

// 적합 여부를 어림하는 상수다. 가중치 크기에 KV 캐시와 런타임 여유를 더한다.
const (
	// overheadPercent는 가중치에 더하는 비율이다(KV 캐시 등).
	overheadPercent = 15
	// gpuFixedMiB는 GPU에서 CUDA 컨텍스트와 작업 버퍼에 남기는 값이다.
	gpuFixedMiB = 768
	// cpuFixedMiB는 CPU 실행에서 운영체제와 데몬에 남기는 값이다.
	cpuFixedMiB = 1024
)

// Input은 추천 계산의 입력이다.
type Input struct {
	// VRAMMiB는 쓸 수 있는 GPU들의 전체 VRAM 합이다. GPU를 못 쓰면 0이다.
	VRAMMiB int64
	// RAMMiB는 시스템 메모리 전체 크기다. 알 수 없으면 0이다.
	RAMMiB int64
}

// Item은 목록의 모델 하나와 이 하드웨어에서의 판단이다.
type Item struct {
	Model
	// Fits는 어림 계산으로 이 하드웨어에 올라가는지다.
	Fits bool `json:"fits"`
	// Placement는 "gpu", "cpu", "none" 중 하나다.
	Placement string `json:"placement"`
	// NeedMiB는 어림한 필요 메모리다.
	NeedMiB int64 `json:"need_mib"`
}

// Result는 추천 결과다.
type Result struct {
	// Basis는 판단 기준이다. "gpu", "cpu", "unknown" 중 하나다.
	Basis string `json:"basis"`
	// BudgetMiB는 기준이 된 메모리 크기다.
	BudgetMiB int64  `json:"budget_mib"`
	Items     []Item `json:"models"`
	// Recommended는 올라가는 가장 큰 모델의 위치다. 없으면 -1이다.
	Recommended int `json:"recommended"`
}

// Recommend는 입력에 맞춰 목록을 판단한다. GPU가 있으면 VRAM, 없으면 RAM이 기준이다.
// CPU 기준은 속도가 느리므로 크기를 더 보수적으로 제한한다(RAM의 절반까지만 가중치로 쓴다).
func Recommend(in Input) Result {
	basis, budget := "unknown", int64(0)
	switch {
	case in.VRAMMiB > 0:
		basis, budget = "gpu", in.VRAMMiB
	case in.RAMMiB > 0:
		basis, budget = "cpu", in.RAMMiB
	}

	items := make([]Item, 0, len(Catalog))
	cat := append([]Model(nil), Catalog...)
	sort.SliceStable(cat, func(i, j int) bool { return cat[i].SizeMiB < cat[j].SizeMiB })
	best := -1
	for i, m := range cat {
		it := Item{Model: m, Placement: "none"}
		switch basis {
		case "gpu":
			it.NeedMiB = m.SizeMiB + m.SizeMiB*overheadPercent/100 + gpuFixedMiB
			it.Fits = it.NeedMiB <= budget
			if it.Fits {
				it.Placement = "gpu"
			}
		case "cpu":
			it.NeedMiB = m.SizeMiB + m.SizeMiB*overheadPercent/100 + cpuFixedMiB
			it.Fits = it.NeedMiB <= budget/2+cpuFixedMiB
			if it.Fits {
				it.Placement = "cpu"
			}
		}
		if it.Fits {
			best = i
		}
		items = append(items, it)
	}
	return Result{Basis: basis, BudgetMiB: budget, Items: items, Recommended: best}
}

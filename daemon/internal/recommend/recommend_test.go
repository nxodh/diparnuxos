package recommend

import "testing"

func TestCatalogIsSortedAndSingleFile(t *testing.T) {
	var prev int64
	for _, m := range Catalog {
		if m.SizeMiB < prev {
			t.Fatalf("catalog is not sorted by size at %s", m.Repo)
		}
		prev = m.SizeMiB
		if m.Repo == "" || m.File == "" || m.License == "" {
			t.Fatalf("incomplete entry: %+v", m)
		}
		if len(m.File) > 5 && m.File[len(m.File)-5:] != ".gguf" {
			t.Fatalf("%s is not a .gguf file", m.File)
		}
	}
}

func TestRecommendGPUPicksLargestFitting(t *testing.T) {
	// 12 GiB: 8B(4693)와 14B(8585)는 올라가고, 27B는 올라가지 않는다.
	r := Recommend(Input{VRAMMiB: 12288})
	if r.Basis != "gpu" || r.Recommended < 0 {
		t.Fatalf("result = %+v", r)
	}
	if got := r.Items[r.Recommended].Params; got != "14B" {
		t.Fatalf("recommended = %s, want 14B", got)
	}
	for _, it := range r.Items {
		if it.Fits && it.Placement != "gpu" {
			t.Fatalf("%s fits but placement = %s", it.Repo, it.Placement)
		}
		if !it.Fits && it.Placement != "none" {
			t.Fatalf("%s does not fit but placement = %s", it.Repo, it.Placement)
		}
	}
}

func TestRecommendSmallVRAMFitsOnlySmallModels(t *testing.T) {
	r := Recommend(Input{VRAMMiB: 2048})
	if r.Recommended < 0 {
		t.Fatal("2 GiB should still fit the smallest model")
	}
	if got := r.Items[r.Recommended].Params; got != "0.5B" {
		t.Fatalf("recommended = %s, want 0.5B", got)
	}
}

func TestRecommendCPUIsMoreConservative(t *testing.T) {
	gpu := Recommend(Input{VRAMMiB: 8192})
	cpu := Recommend(Input{RAMMiB: 8192})
	if cpu.Basis != "cpu" {
		t.Fatalf("basis = %s", cpu.Basis)
	}
	if cpu.Recommended > gpu.Recommended {
		t.Fatalf("cpu recommended index %d is larger than gpu %d", cpu.Recommended, gpu.Recommended)
	}
	for _, it := range cpu.Items {
		if it.Fits && it.Placement != "cpu" {
			t.Fatalf("%s placement = %s", it.Repo, it.Placement)
		}
	}
}

func TestRecommendUnknownHardware(t *testing.T) {
	r := Recommend(Input{})
	if r.Basis != "unknown" || r.Recommended != -1 {
		t.Fatalf("result = %+v", r)
	}
	for _, it := range r.Items {
		if it.Fits || it.Placement != "none" {
			t.Fatalf("%s must not fit with unknown hardware", it.Repo)
		}
	}
}

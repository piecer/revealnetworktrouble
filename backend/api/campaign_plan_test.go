package api

import (
	"os"
	"regexp"
	"reflect"
	"testing"
)

func TestContinuousImprovementPlanKeepsStageOrder(t *testing.T) {
	content, err := os.ReadFile("../../.hermes/plans/2026-09-01_201624-checknetwork-continuous-improvement.md")
	if err != nil {
		t.Fatalf("read campaign plan: %v", err)
	}

	matches := regexp.MustCompile(`(?m)^## Stage [1-7]: (.+)$`).FindAllStringSubmatch(string(content), -1)
	got := make([]string, 0, len(matches))
	for _, match := range matches {
		got = append(got, match[1])
	}
	want := []string{
		"실행 경계와 HTTPS 판정 복구",
		"설명 가능한 자동 분석 계약",
		"Web 요청 lifecycle과 분석 워크스페이스 UI",
		"Web topology 정확성과 대용량 성능",
		"Android 기능 동등성과 lifecycle 아키텍처",
		"외부 보강·관측성·운영 성능 강화",
		"교차 플랫폼 재감사와 다음 루프 계획",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("campaign stage order changed\n got: %#v\nwant: %#v", got, want)
	}
}

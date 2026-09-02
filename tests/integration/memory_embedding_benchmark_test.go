package integration

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/llm"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
)

type benchmarkCandidateModel struct {
	name             string
	queryInstruction string
}

var benchmarkFacts = []string{
	"이샘플은 플랫폼 팀 소속이다.",
	"이샘플은 회의 요약을 짧은 불릿 포인트로 받기를 선호한다.",
	"이샘플은 2026-09-11까지 휴가 중이다.",
	"이샘플은 매주 화요일 10시 스탠드업을 진행한다.",
	"이샘플은 배포 공지를 영어로 쓰는 것을 싫어한다.",
	"박예시는 데이터 팀의 리드다.",
	"박예시는 분기 리뷰 문서를 담당한다.",
	"박예시는 오전 회의를 피하고 싶어 한다.",
	"박예시는 슬랙보다 메일로 정리된 요청을 선호한다.",
	"박예시는 지난달 결제 서비스 장애 회고를 주도했다.",
	"최견본은 재무 팀에서 예산 승인을 담당한다.",
	"최견본은 법인카드 정산 마감을 매월 5일로 정했다.",
	"최견본은 숫자는 표로 정리해 달라고 요청했다.",
	"최견본은 2026-10-01부터 두 달간 육아휴직이다.",
	"플랫폼 팀의 3분기 목표는 admind 설정을 중앙 플레인으로 옮기는 것이다.",
	"회사 전체 회의는 매월 첫째 주 목요일 오후 4시다.",
	"사무실 주차는 지하 2층만 사용할 수 있다.",
	"고객사 데모는 항상 스테이징 환경에서만 진행한다.",
	"긴급 장애는 오케이 채널이 아니라 온콜 채널에 올린다.",
	"신규 입사자 온보딩 문서는 위키의 people 폴더에 있다.",
	"이샘플은 파이썬보다 고 언어로 작성된 예제를 원한다.",
	"박예시는 프로젝트 이름을 항상 한국어 제목으로 붙이길 원한다.",
	"최견본은 지출 보고서에 환율 기준일을 반드시 적으라고 했다.",
	"데이터 팀은 매주 금요일 오후에 데이터 품질 점검을 한다.",
	"플랫폼 팀 회식은 분기마다 한 번, 주로 판교에서 한다.",
	"이샘플은 지난주에 Jetson 장비 두 대를 반납했다.",
	"박예시는 대시보드 색상은 회사 브랜드 팔레트만 쓰라고 했다.",
	"최견본은 회의 초대에 안건이 없으면 참석하지 않는다.",
	"회사 VPN은 외부 네트워크에서 관리자 화면에 들어갈 때만 필요하다.",
	"이샘플은 답장에 이모지를 쓰지 말라고 했다.",
}

var benchmarkQueries = []struct {
	query    string
	relevant []int
}{
	{"이샘플은 어느 팀이야?", []int{0}},
	{"이샘플한테 요약 보낼 때 형식", []int{1}},
	{"이샘플 언제 돌아와?", []int{2}},
	{"스탠드업 몇 시에 해?", []int{3}},
	{"배포 공지 어떤 언어로 써야 해?", []int{4}},
	{"데이터 팀 리드가 누구지?", []int{5}},
	{"분기 리뷰 문서 누가 맡았어?", []int{6}},
	{"박예시랑 회의 잡을 때 피할 시간", []int{7}},
	{"박예시한테 요청은 어떻게 보내는 게 좋아?", []int{8}},
	{"예산 승인은 누구한테 받아?", []int{10}},
	{"법인카드 정산 마감일", []int{11}},
	{"최견본한테 보고서 낼 때 숫자 형식", []int{12, 22}},
	{"최견본 육아휴직 기간", []int{13}},
	{"플랫폼 팀 이번 분기 목표", []int{14}},
	{"전체 회의 언제야?", []int{15}},
	{"주차 어디에 해?", []int{16}},
	{"장애 났을 때 어디에 알려?", []int{18}},
	{"온보딩 문서 어디 있어?", []int{19}},
	{"이샘플 코드 예제 언어 취향", []int{20}},
	{"이샘플 답장 스타일", []int{1, 29}},
}

// Measures, on demand, how well each candidate embedding model retrieves
// Korean memory facts for Korean questions at the 1,024 dimensions the store
// keeps. It spends a few cents on OpenRouter and prints a table; it decides
// nothing by itself.
func TestMemoryEmbeddingBenchmark(t *testing.T) {
	if os.Getenv("BLUECLAW_EMBEDDING_BENCHMARK") != "1" {
		t.Skip("set BLUECLAW_EMBEDDING_BENCHMARK=1 to run the embedding benchmark")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is required for the embedding benchmark")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	candidates := []benchmarkCandidateModel{
		{name: "qwen/qwen3-embedding-8b", queryInstruction: qwenQueryInstruction},
		{name: "qwen/qwen3-embedding-4b", queryInstruction: qwenQueryInstruction},
		{name: "perplexity/pplx-embed-v1-4b"},
		{name: "perplexity/pplx-embed-v1-0.6b"},
	}
	t.Logf("%-32s %8s %8s %8s %8s", "model", "R@1", "R@3", "MRR", "seconds")
	for _, candidate := range candidates {
		started := time.Now()
		recallAt1, recallAt3, meanReciprocalRank, errorValue := benchmarkEmbeddingModel(ctx, apiKey, candidate)
		if errorValue != nil {
			t.Logf("%-32s failed: %v", candidate.name, errorValue)
			continue
		}
		t.Logf("%-32s %8.3f %8.3f %8.3f %8.1f", candidate.name, recallAt1, recallAt3, meanReciprocalRank, time.Since(started).Seconds())
	}
}

func benchmarkEmbeddingModel(ctx context.Context, apiKey string, candidate benchmarkCandidateModel) (float64, float64, float64, error) {
	client := llm.OpenAIEmbeddingClient{Endpoint: openRouterEmbeddingsURL, APIKey: apiKey, ModelName: candidate.name, Dimensions: memory.EmbeddingDimensionCount}
	factEmbeddings := make([][]float32, 0, len(benchmarkFacts))
	for _, fact := range benchmarkFacts {
		embedding, errorValue := client.GenerateEmbedding(ctx, fact)
		if errorValue != nil {
			return 0, 0, 0, fmt.Errorf("fact embedding: %w", errorValue)
		}
		factEmbeddings = append(factEmbeddings, embedding)
	}
	var hitsAt1, hitsAt3, reciprocalRankSum float64
	for _, query := range benchmarkQueries {
		queryEmbedding, errorValue := client.GenerateEmbedding(ctx, candidate.queryInstruction+query.query)
		if errorValue != nil {
			return 0, 0, 0, fmt.Errorf("query embedding: %w", errorValue)
		}
		ranking := rankBySimilarity(queryEmbedding, factEmbeddings)
		bestRank := math.MaxInt
		for _, relevantIndex := range query.relevant {
			for rank, factIndex := range ranking {
				if factIndex == relevantIndex && rank < bestRank {
					bestRank = rank
				}
			}
		}
		if bestRank == 0 {
			hitsAt1++
		}
		if bestRank < 3 {
			hitsAt3++
		}
		reciprocalRankSum += 1 / float64(bestRank+1)
	}
	queryCount := float64(len(benchmarkQueries))
	return hitsAt1 / queryCount, hitsAt3 / queryCount, reciprocalRankSum / queryCount, nil
}

func rankBySimilarity(queryEmbedding []float32, factEmbeddings [][]float32) []int {
	similarities := make([]float64, len(factEmbeddings))
	for index, factEmbedding := range factEmbeddings {
		similarities[index] = cosine(queryEmbedding, factEmbedding)
	}
	ranking := make([]int, len(factEmbeddings))
	for index := range ranking {
		ranking[index] = index
	}
	sort.SliceStable(ranking, func(left int, right int) bool { return similarities[ranking[left]] > similarities[ranking[right]] })
	return ranking
}

func cosine(left []float32, right []float32) float64 {
	var dot, leftNorm, rightNorm float64
	for index := range left {
		dot += float64(left[index]) * float64(right[index])
		leftNorm += float64(left[index]) * float64(left[index])
		rightNorm += float64(right[index]) * float64(right[index])
	}
	if leftNorm == 0 || rightNorm == 0 {
		return -1
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

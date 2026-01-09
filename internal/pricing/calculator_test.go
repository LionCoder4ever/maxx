package pricing

import (
	"testing"

	"github.com/Bowl42/maxx-next/internal/usage"
)

func TestCalculateTieredCostMicro(t *testing.T) {
	// 测试: $3/M tokens, 阈值 200K, 超阈值倍率 2/1
	basePriceMicro := uint64(3_000_000) // $3/M

	tests := []struct {
		name     string
		tokens   uint64
		expected uint64
	}{
		{
			name:     "below threshold 100K",
			tokens:   100_000,
			expected: 300_000, // 100K × $3/M = $0.30 = 300,000 microUSD
		},
		{
			name:     "at threshold 200K",
			tokens:   200_000,
			expected: 600_000, // 200K × $3/M = $0.60 = 600,000 microUSD
		},
		{
			name:     "above threshold 300K",
			tokens:   300_000,
			expected: 1_200_000, // 200K × $3/M + 100K × $3/M × 2 = $0.60 + $0.60 = 1,200,000 microUSD
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateTieredCostMicro(tt.tokens, basePriceMicro, 2, 1, 200_000)
			if got != tt.expected {
				t.Errorf("CalculateTieredCostMicro() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestCalculateLinearCostMicro(t *testing.T) {
	tests := []struct {
		name       string
		tokens     uint64
		priceMicro uint64
		expected   uint64
	}{
		{
			name:       "1M tokens at $3/M",
			tokens:     1_000_000,
			priceMicro: 3_000_000,
			expected:   3_000_000, // $3
		},
		{
			name:       "100K tokens at $15/M",
			tokens:     100_000,
			priceMicro: 15_000_000,
			expected:   1_500_000, // $1.50
		},
		{
			name:       "50K tokens at $0.30/M (cache read)",
			tokens:     50_000,
			priceMicro: 300_000, // $0.30/M = $3/M × 0.1
			expected:   15_000,  // $0.015
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateLinearCostMicro(tt.tokens, tt.priceMicro)
			if got != tt.expected {
				t.Errorf("CalculateLinearCostMicro() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestCalculator_Calculate(t *testing.T) {
	calc := GlobalCalculator()

	tests := []struct {
		name     string
		model    string
		metrics  *usage.Metrics
		wantZero bool
	}{
		{
			name:  "claude-sonnet-4 basic",
			model: "claude-sonnet-4-20250514",
			metrics: &usage.Metrics{
				InputTokens:  100_000,
				OutputTokens: 10_000,
			},
			wantZero: false,
		},
		{
			name:  "gpt-4o basic",
			model: "gpt-4o-2024-05-13",
			metrics: &usage.Metrics{
				InputTokens:  50_000,
				OutputTokens: 5_000,
			},
			wantZero: false,
		},
		{
			name:  "unknown model",
			model: "unknown-model-xyz",
			metrics: &usage.Metrics{
				InputTokens:  100_000,
				OutputTokens: 10_000,
			},
			wantZero: true,
		},
		{
			name:     "nil metrics",
			model:    "claude-sonnet-4",
			metrics:  nil,
			wantZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calc.Calculate(tt.model, tt.metrics)
			if tt.wantZero && got != 0 {
				t.Errorf("Calculate() = %d, want 0", got)
			}
			if !tt.wantZero && got == 0 {
				t.Errorf("Calculate() = 0, want non-zero")
			}
		})
	}
}

func TestCalculator_Calculate_WithCache(t *testing.T) {
	calc := GlobalCalculator()

	// Claude Sonnet 4: input=$3/M, output=$15/M
	// Cache read: $3/M / 10 = $0.30/M
	// Cache 5m write: $3/M * 5/4 = $3.75/M
	// Cache 1h write: $3/M * 2 = $6/M
	metrics := &usage.Metrics{
		InputTokens:          100_000, // 100K × $3/M = $0.30 = 300,000 microUSD
		OutputTokens:         10_000,  // 10K × $15/M = $0.15 = 150,000 microUSD
		CacheReadCount:       50_000,  // 50K × $0.30/M = $0.015 = 15,000 microUSD
		Cache5mCreationCount: 20_000,  // 20K × $3.75/M = $0.075 = 75,000 microUSD
		Cache1hCreationCount: 10_000,  // 10K × $6/M = $0.06 = 60,000 microUSD
	}

	cost := calc.Calculate("claude-sonnet-4", metrics)
	if cost == 0 {
		t.Fatal("Calculate() = 0, want non-zero")
	}

	// Expected: 300,000 + 150,000 + 15,000 + 75,000 + 60,000 = 600,000 microUSD
	expectedMicroUSD := uint64(600_000)
	if cost != expectedMicroUSD {
		t.Errorf("Calculate() = %d microUSD, want %d microUSD", cost, expectedMicroUSD)
	}
}

func TestCalculator_Calculate_1MContext(t *testing.T) {
	calc := GlobalCalculator()

	// Claude Sonnet 4 with 1M context: 超过 200K 时 input×2, output×1.5
	// input: $3/M, output: $15/M
	metrics := &usage.Metrics{
		InputTokens:  300_000, // 200K×$3 + 100K×$3×2 = $0.6 + $0.6 = $1.2 = 1,200,000 microUSD
		OutputTokens: 50_000,  // 全部低于 200K: 50K×$15/M = $0.75 = 750,000 microUSD
	}

	cost := calc.Calculate("claude-sonnet-4", metrics)
	expectedMicroUSD := uint64(1_200_000 + 750_000)
	if cost != expectedMicroUSD {
		t.Errorf("Calculate() = %d microUSD, want %d microUSD", cost, expectedMicroUSD)
	}
}

func TestPriceTable_Get_PrefixMatch(t *testing.T) {
	pt := DefaultPriceTable()

	tests := []struct {
		modelID   string
		wantFound bool
	}{
		{"claude-sonnet-4", true},
		{"claude-sonnet-4-20250514", true},   // prefix match
		{"claude-sonnet-4-5-20250514", true}, // prefix match
		{"gpt-4o", true},
		{"gpt-4o-2024-05-13", true}, // prefix match
		{"unknown-model", false},
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			pricing := pt.Get(tt.modelID)
			if tt.wantFound && pricing == nil {
				t.Errorf("Get(%s) = nil, want non-nil", tt.modelID)
			}
			if !tt.wantFound && pricing != nil {
				t.Errorf("Get(%s) = %v, want nil", tt.modelID, pricing)
			}
		})
	}
}

func TestCacheDefaultPrices(t *testing.T) {
	// 验证缓存价格的默认计算
	pricing := &ModelPricing{
		InputPriceMicro:  3_000_000, // $3/M
		OutputPriceMicro: 15_000_000,
	}

	// cache read: input / 10 = $0.30/M = 300,000 microUSD/M
	if got := pricing.GetEffectiveCacheReadPriceMicro(); got != 300_000 {
		t.Errorf("GetEffectiveCacheReadPriceMicro() = %d, want 300000", got)
	}

	// cache 5m write: input * 5/4 = $3.75/M = 3,750,000 microUSD/M
	if got := pricing.GetEffectiveCache5mWritePriceMicro(); got != 3_750_000 {
		t.Errorf("GetEffectiveCache5mWritePriceMicro() = %d, want 3750000", got)
	}

	// cache 1h write: input * 2 = $6/M = 6,000,000 microUSD/M
	if got := pricing.GetEffectiveCache1hWritePriceMicro(); got != 6_000_000 {
		t.Errorf("GetEffectiveCache1hWritePriceMicro() = %d, want 6000000", got)
	}
}

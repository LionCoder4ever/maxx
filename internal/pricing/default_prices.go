package pricing

import "sync"

var (
	defaultTable *PriceTable
	defaultOnce  sync.Once
)

// DefaultPriceTable 返回默认价格表（单例）
func DefaultPriceTable() *PriceTable {
	defaultOnce.Do(func() {
		defaultTable = initDefaultPrices()
	})
	return defaultTable
}

// 价格常量 (microUSD/M tokens)
// $1/M = 1,000,000 microUSD/M
const (
	usd1   = 1_000_000   // $1/M
	usd3   = 3_000_000   // $3/M
	usd15  = 15_000_000  // $15/M
	usd75  = 75_000_000  // $75/M
	usd150 = 150_000_000 // $150/M
	usd600 = 600_000_000 // $600/M
)

// initDefaultPrices 初始化默认价格
func initDefaultPrices() *PriceTable {
	pt := NewPriceTable("2025.01")

	// Claude 4 系列
	pt.Set(&ModelPricing{
		ModelID:          "claude-sonnet-4-5",
		InputPriceMicro:  usd3,
		OutputPriceMicro: usd15,
		Has1MContext:     true,
	})
	pt.Set(&ModelPricing{
		ModelID:          "claude-sonnet-4",
		InputPriceMicro:  usd3,
		OutputPriceMicro: usd15,
		Has1MContext:     true,
	})
	pt.Set(&ModelPricing{
		ModelID:          "claude-opus-4",
		InputPriceMicro:  usd15,
		OutputPriceMicro: usd75,
	})

	// Claude 3.5 系列
	pt.Set(&ModelPricing{
		ModelID:          "claude-3-5-sonnet",
		InputPriceMicro:  usd3,
		OutputPriceMicro: usd15,
	})
	pt.Set(&ModelPricing{
		ModelID:          "claude-3-5-haiku",
		InputPriceMicro:  800_000,  // $0.80/M
		OutputPriceMicro: 4_000_000, // $4/M
	})

	// Claude 3 系列
	pt.Set(&ModelPricing{
		ModelID:          "claude-3-opus",
		InputPriceMicro:  usd15,
		OutputPriceMicro: usd75,
	})
	pt.Set(&ModelPricing{
		ModelID:          "claude-3-sonnet",
		InputPriceMicro:  usd3,
		OutputPriceMicro: usd15,
	})
	pt.Set(&ModelPricing{
		ModelID:          "claude-3-haiku",
		InputPriceMicro:  250_000,   // $0.25/M
		OutputPriceMicro: 1_250_000, // $1.25/M
	})

	// Gemini 系列
	pt.Set(&ModelPricing{
		ModelID:          "gemini-2.5-pro",
		InputPriceMicro:  1_250_000,  // $1.25/M
		OutputPriceMicro: 10_000_000, // $10/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gemini-2.5-flash",
		InputPriceMicro:  150_000, // $0.15/M
		OutputPriceMicro: 600_000, // $0.60/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gemini-2.0-flash",
		InputPriceMicro:  100_000, // $0.10/M
		OutputPriceMicro: 400_000, // $0.40/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gemini-1.5-pro",
		InputPriceMicro:  1_250_000, // $1.25/M
		OutputPriceMicro: 5_000_000, // $5/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gemini-1.5-flash",
		InputPriceMicro:  75_000,  // $0.075/M
		OutputPriceMicro: 300_000, // $0.30/M
	})

	// OpenAI GPT 系列
	pt.Set(&ModelPricing{
		ModelID:          "gpt-4o",
		InputPriceMicro:  2_500_000,  // $2.50/M
		OutputPriceMicro: 10_000_000, // $10/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gpt-4o-mini",
		InputPriceMicro:  150_000, // $0.15/M
		OutputPriceMicro: 600_000, // $0.60/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gpt-4-turbo",
		InputPriceMicro:  10_000_000, // $10/M
		OutputPriceMicro: 30_000_000, // $30/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gpt-4",
		InputPriceMicro:  30_000_000, // $30/M
		OutputPriceMicro: 60_000_000, // $60/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "gpt-3.5-turbo",
		InputPriceMicro:  500_000,   // $0.50/M
		OutputPriceMicro: 1_500_000, // $1.50/M
	})

	// OpenAI o 系列
	pt.Set(&ModelPricing{
		ModelID:          "o1",
		InputPriceMicro:  usd15,
		OutputPriceMicro: 60_000_000, // $60/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "o1-mini",
		InputPriceMicro:  usd3,
		OutputPriceMicro: 12_000_000, // $12/M
	})
	pt.Set(&ModelPricing{
		ModelID:          "o1-pro",
		InputPriceMicro:  usd150,
		OutputPriceMicro: usd600,
	})
	pt.Set(&ModelPricing{
		ModelID:          "o3-mini",
		InputPriceMicro:  1_100_000, // $1.10/M
		OutputPriceMicro: 4_400_000, // $4.40/M
	})

	return pt
}

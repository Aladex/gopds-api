package llmeval

import "math"

// The gate's error bound: a one-sided 95% upper confidence bound on the share
// of wrongly accepted items, at most 0.5%.
const (
	// MaxErrorShare is the largest admissible error share, for the count
	// bound and the credit-weighted share alike.
	MaxErrorShare = 0.005
	// boundAlpha is one minus the bound's confidence.
	boundAlpha = 0.05
	// bisectHalf splits the bisection interval.
	bisectHalf = 0.5
)

// binomialCDF is P(X <= k) for X ~ Binomial(n, p), summed in log space.
func binomialCDF(k, n int, p float64) float64 {
	if p <= 0 {
		return 1
	}
	if p >= 1 {
		if k >= n {
			return 1
		}
		return 0
	}
	lgN, _ := math.Lgamma(float64(n + 1))
	logP, logQ := math.Log(p), math.Log1p(-p)
	sum := 0.0
	for i := 0; i <= k && i <= n; i++ {
		lgI, _ := math.Lgamma(float64(i + 1))
		lgNI, _ := math.Lgamma(float64(n - i + 1))
		sum += math.Exp(lgN - lgI - lgNI + float64(i)*logP + float64(n-i)*logQ)
	}
	return math.Min(sum, 1)
}

// ClopperPearsonUpper is the exact one-sided 95% upper confidence bound on a
// binomial share with k successes in n trials: the p at which observing at
// most k has probability 5%. With n = 0 nothing is known and the bound is 1.
func ClopperPearsonUpper(k, n int) float64 {
	if n <= 0 || k >= n {
		return 1
	}
	lo, hi := 0.0, 1.0
	for range 200 {
		mid := lo + (hi-lo)*bisectHalf
		if binomialCDF(k, n, mid) > boundAlpha {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// PassesBound reports whether k errors among n accepted items keep the upper
// bound within MaxErrorShare: 0 errors from 598 items on, 1 from 947, 2 from
// 1 258.
func PassesBound(k, n int) bool {
	return n > 0 && ClopperPearsonUpper(k, n) <= MaxErrorShare
}

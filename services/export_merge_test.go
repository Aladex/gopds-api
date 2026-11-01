package services

// MergeEstimateTokensOf is a merge eval's estimate of the given frozen
// counts and input bytes at sizes, for the external tests.
func MergeEstimateTokensOf(sizes AuthorLLMMergeEvalSizes, holdout, suite, anchors, holdoutBytes, suiteBytes int) int64 {
	return mergeEstimateTokens(&mergeEvalCounts{holdout: holdout, suite: suite, anchors: anchors,
		holdoutBytes: holdoutBytes, suiteBytes: suiteBytes}, &sizes)
}

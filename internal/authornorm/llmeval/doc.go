// Package llmeval turns the answers of an eval run into its report and
// decision (design K, section 8). It is pure: the service collects the frozen
// items and every participant's answers from the database and hands them over;
// nothing here reads a clock, a file or a database.
//
// What it measures is the production pair's agreement with the consensus of
// two judges, not truth. An item the pair agrees on is accepted when the
// judges' gold confirms it and an error when the gold differs; without a gold
// (the judges disagree, refuse or did not answer) it is contested — neither
// accepted nor an error, only counted, since resolve applies only what both
// judges confirm. A stratum passes when the one-sided 95% Clopper–Pearson upper
// bound of the error share is at most 0.5% and the credit-weighted error share
// is at most 0.5% too — and only if the run as a whole holds: the fingerprint
// stage passed, every participant matched the anchor set (judges 99%, production
// 98%), changed at most 10% of its repeated answers and returned at most 2%
// invalid replies. The repeat check catches a broken or swapped participant,
// not honest ambiguity: an unstable answer is not applied, so it costs
// coverage, not correctness.
package llmeval

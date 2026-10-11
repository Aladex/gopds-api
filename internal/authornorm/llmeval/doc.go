// Package llmeval turns the answers of an eval run into its report and
// decision (design K, section 8). It is pure: the service collects the frozen
// items and every participant's answers from the database and hands them over;
// nothing here reads a clock, a file or a database.
//
// What it measures is the production pair's agreement with the consensus of
// two judges, not truth. An item the pair agrees on is accepted; it is an error
// when its answer differs from the judges' gold or the judges disagree
// (contested). A stratum passes when the one-sided 95% Clopper–Pearson upper
// bound of the error share is at most 0.5% and the credit-weighted error share
// is at most 0.5% too — and only if the run as a whole holds: the fingerprint
// stage passed, every participant matched the anchor set (judges 99%, production
// 98%), reproduced itself (at most 1% changed answers) and returned at most 2%
// invalid replies.
package llmeval

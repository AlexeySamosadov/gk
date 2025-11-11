Phases:

1. Implement logic in inference-chain, update onlu query interfaces at decentralized api \
Result:
- all unit test ` go test -count=1 ./... ` are passed for both projets
- testermint test passes (will run in CICD but check structures for query is they are used there)
- decentralized api not used


2. Decentralized api is implemented 
- all unit test ` go test -count=1 ./... ` are passed for both projets
- testermint test passes with default value of expected_confirmations_per_epoch=0 (=> ideantical behaviour with now) (will run in CICD but check structures for query is they are used there)


3. Testermint test is implemented and phase transition bug fixed 
- new testermint test with expected_confirmations_per_epoch=2 is implemented and WORKING
- two tests created: confirmation passed (same rewards) and confirmation failed (capped rewards)
- tests verify full flow including reward settlement at epoch end
- data structures added to testermint: ConfirmationPoCEvent, ConfirmationPoCPhase, ConfirmationPoCParams
- EpochResponse updated to include is_confirmation_poc_active and active_confirmation_poc_event fields
- helper functions implemented for: waitForConfirmationPoCTrigger, waitForConfirmationPoCPhase

Test implementation details:
- Tests use standard initialization: 3 participants (genesis + 2 joins), each with 1 default ML node
- Test 1 (confirmation passed): All 3 participants return confirmation weight=10 (same as regular), verifies rewards are not affected
- Test 2 (confirmation failed/capped): Join1 returns confirmation_weight=8 < regular_weight=10 but above alpha_threshold=7, verifies rewards are capped and no slashing occurs
- Tests use spec builder to configure: epochLength=100, epochShift=80, expectedConfirmationsPerEpoch=2
- Tests wait for confirmation PoC phases: GRACE_PERIOD, GENERATION, VALIDATION, COMPLETED

CRITICAL BUG FIX (see phase-transition-bug-fix.md):
- Phase transitions were using exact block match (==) instead of >= 
- This caused confirmation PoC to get stuck in GRACE_PERIOD forever
- Fixed all three transitions to use >= instead of ==
- Added blockHeight to transition logs for debugging

Test approach (see testermint-fix-summary.md for details):
- Use standard testermint pattern: initCluster(joinCount=2, mergeSpec=confirmationSpec)
- Use default epoch params (15 blocks) via mergeSpec - includes ALL required PoC timing parameters
- No custom ML node setup needed - default 1 node per participant is sufficient
- Mock PoC responses control confirmation weights, not number of nodes
- CRITICAL: Must use mergeSpec (not genesisSpec override) to preserve pocStageDuration, pocValidationDuration, etc.
- Follows same pattern as ValidationTests, BLSDKGSuccessTest, and other working tests 
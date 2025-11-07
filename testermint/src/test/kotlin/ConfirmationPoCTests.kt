import com.productscience.*
import com.productscience.data.*
import org.assertj.core.api.Assertions.assertThat
import org.assertj.core.data.Offset
import org.assertj.core.data.Percentage
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.Timeout
import org.tinylog.kotlin.Logger
import java.util.concurrent.TimeUnit

@Timeout(value = 20, unit = TimeUnit.MINUTES)
class ConfirmationPoCTests : TestermintTest() {
    
    @Test
    fun `confirmation PoC passed - same rewards`() {
        logSection("=== TEST: Confirmation PoC Passed - Same Rewards ===")
        
        // Initialize cluster with custom spec for confirmation PoC testing
        // Configure epoch timing to allow confirmation PoC triggers during inference phase
        val confirmationSpec = createConfirmationPoCSpec(expectedConfirmationsPerEpoch = 100)
        val (cluster, genesis) = initCluster(
            joinCount = 2,
            mergeSpec = confirmationSpec,  // Merge with defaults instead of replacing
            reboot = true
        )
        
        logSection("✅ Cluster Initialized Successfully!")
        
        val join1 = cluster.joinPairs[0]
        val join2 = cluster.joinPairs[1]
        
        logSection("Verifying cluster initialized with 3 participants")
        val allPairs = listOf(genesis, join1, join2)
        assertThat(allPairs).hasSize(3)
        
        logSection("Waiting for first PoC cycle to establish regular weights")
        genesis.waitForStage(EpochStage.START_OF_POC)
        genesis.waitForStage(EpochStage.CLAIM_REWARDS, offset = 2)
        
        val initialStats = genesis.node.getParticipantCurrentStats()
        logSection("Initial participant weights:")
        initialStats.participantCurrentStats?.forEach {
            Logger.info("  ${it.participantId}: weight=${it.weight}")
        }
        
        logSection("Setting PoC mocks for confirmation (same weight=10)")
        genesis.mock?.setPocResponse(10)
        genesis.mock?.setPocValidationResponse(10)
        join1.mock?.setPocResponse(10)
        join1.mock?.setPocValidationResponse(10)
        join2.mock?.setPocResponse(10)
        join2.mock?.setPocValidationResponse(10)
        
        logSection("Waiting for confirmation PoC trigger during inference phase")
        val confirmationEvent = waitForConfirmationPoCTrigger(genesis)
        assertThat(confirmationEvent).isNotNull
        Logger.info("Confirmation PoC triggered at height ${confirmationEvent!!.triggerHeight}")
        
        logSection("Waiting for confirmation PoC generation phase")
        waitForConfirmationPoCPhase(genesis, ConfirmationPoCPhase.CONFIRMATION_POC_GENERATION)
        Logger.info("Confirmation PoC generation phase active")
        
        logSection("Waiting for confirmation PoC validation phase")
        waitForConfirmationPoCPhase(genesis, ConfirmationPoCPhase.CONFIRMATION_POC_VALIDATION)
        Logger.info("Confirmation PoC validation phase active")
        
        logSection("Waiting for confirmation PoC completion")
        waitForConfirmationPoCCompletion(genesis)
        Logger.info("Confirmation PoC completed (event cleared)")
        
        logSection("Waiting for NEXT epoch where confirmation weights will be applied")
        // Confirmation weights are only calculated and applied during the next epoch's settlement
        genesis.waitForStage(EpochStage.START_OF_POC)
        Logger.info("New epoch started, confirmation weights will be used in settlement")
        
        // Record balances AFTER confirmation but BEFORE settlement
        val initialBalances = mapOf(
            genesis.node.getColdAddress() to genesis.node.getSelfBalance(),
            join1.node.getColdAddress() to join1.node.getSelfBalance(),
            join2.node.getColdAddress() to join2.node.getSelfBalance()
        )
        
        logSection("Waiting for reward settlement with confirmation weights")
        genesis.waitForStage(EpochStage.CLAIM_REWARDS, offset = 2)
        
        logSection("Verifying rewards are calculated using full weight")
        val finalBalances = mapOf(
            genesis.node.getColdAddress() to genesis.node.getSelfBalance(),
            join1.node.getColdAddress() to join1.node.getSelfBalance(),
            join2.node.getColdAddress() to join2.node.getSelfBalance()
        )
        
        // All participants should have received rewards based on their full weight
        val balanceChanges = mutableListOf<Long>()
        finalBalances.forEach { (address, finalBalance) ->
            val initialBalance = initialBalances[address]!!
            val change = finalBalance - initialBalance
            balanceChanges.add(change)
            Logger.info("  $address: balance change = $change")
            // Should have positive reward (not capped since confirmation weight matches regular weight)
            assertThat(change).isGreaterThan(0)
        }
        
        // All participants have same weight (10) and same confirmation weight (10)
        // So they should receive identical rewards
        logSection("Verifying all balance changes are identical")
        val expectedChange = balanceChanges[0]
        balanceChanges.forEach { change ->
            assertThat(change.toDouble()).isCloseTo(expectedChange.toDouble(), Percentage.withPercentage(1.0))
        }
        Logger.info("  All participants received identical rewards: $expectedChange")
        
        logSection("TEST PASSED: Confirmation PoC with same weight does not affect rewards")
    }
    
    @Test
    fun `confirmation PoC failed - capped rewards`() {
        logSection("=== TEST: Confirmation PoC Failed - Capped Rewards ===")
        
        // Initialize cluster with custom spec for confirmation PoC testing
        // Configure epoch timing to allow confirmation PoC triggers during inference phase
        val confirmationSpec = createConfirmationPoCSpec(expectedConfirmationsPerEpoch = 100)
        val (cluster, genesis) = initCluster(
            joinCount = 2,
            mergeSpec = confirmationSpec,  // Merge with defaults instead of replacing
            reboot = true
        )
        
        logSection("✅ Cluster Initialized Successfully!")
        
        val join1 = cluster.joinPairs[0]
        val join2 = cluster.joinPairs[1]
        
        logSection("Verifying cluster initialized with 3 participants")
        val allPairs = listOf(genesis, join1, join2)
        assertThat(allPairs).hasSize(3)
        
        logSection("Waiting for first PoC cycle to establish regular weights")
        genesis.waitForStage(EpochStage.START_OF_POC)
        genesis.waitForStage(EpochStage.CLAIM_REWARDS, offset = 2)
        
        val initialStats = genesis.node.getParticipantCurrentStats()
        logSection("Initial participant weights:")
        initialStats.participantCurrentStats?.forEach {
            Logger.info("  ${it.participantId}: weight=${it.weight}")
        }
        
        logSection("Waiting for confirmation PoC trigger during inference phase")
        val confirmationEvent = waitForConfirmationPoCTrigger(genesis)
        assertThat(confirmationEvent).isNotNull
        Logger.info("Confirmation PoC triggered at height ${confirmationEvent!!.triggerHeight}")

        logSection("Setting PoC mocks for confirmation")
        Logger.info("  Genesis: weight=10 (passes)")
        Logger.info("  Join1: weight=8 (fails but above alpha=7, no slashing)")
        Logger.info("  Join2: weight=10 (passes)")
        genesis.mock?.setPocResponse(10)
        genesis.mock?.setPocValidationResponse(10)
        join1.mock?.setPocResponse(8)  // Lower weight, but above alpha threshold (0.70 * 10 = 7)
        join1.mock?.setPocValidationResponse(8)
        join2.mock?.setPocResponse(10)
        join2.mock?.setPocValidationResponse(10)

        logSection("Waiting for confirmation PoC generation phase")
        waitForConfirmationPoCPhase(genesis, ConfirmationPoCPhase.CONFIRMATION_POC_GENERATION)
        Logger.info("Confirmation PoC generation phase active")
        
        logSection("Waiting for confirmation PoC validation phase")
        waitForConfirmationPoCPhase(genesis, ConfirmationPoCPhase.CONFIRMATION_POC_VALIDATION)
        Logger.info("Confirmation PoC validation phase active")
        
        logSection("Waiting for confirmation PoC completion")
        waitForConfirmationPoCCompletion(genesis)
        Logger.info("Confirmation PoC completed (event cleared)")
        genesis.mock?.setPocResponse(10)
        genesis.mock?.setPocValidationResponse(10)
        join1.mock?.setPocResponse(10)  // Lower weight, but above alpha threshold (0.70 * 10 = 7)
        join1.mock?.setPocValidationResponse(10)
        join2.mock?.setPocResponse(10)
        join2.mock?.setPocValidationResponse(10)
        
        logSection("Verifying no slashing occurred for Join1 (above alpha threshold)")
        val join1Address = join1.node.getColdAddress()
        val validatorsAfterPoC = genesis.node.getValidators()
        val join1ValidatorAfterPoC = validatorsAfterPoC.validators.find { 
            it.consensusPubkey.value == join1.node.getValidatorInfo().key 
        }
        assertThat(join1ValidatorAfterPoC).isNotNull
        assertThat(join1ValidatorAfterPoC!!.status).isEqualTo(StakeValidatorStatus.BONDED.value)
        Logger.info("  Join1 is still bonded (not slashed, confirmation_weight=8 > alpha*regular_weight=7)")
        
        logSection("Waiting for NEXT epoch where confirmation weights will be applied")
        // Confirmation weights are only calculated and applied during the next epoch's settlement
        genesis.waitForStage(EpochStage.START_OF_POC)
        Logger.info("New epoch started, confirmation weights will be used in settlement")
        
        // Record balances AFTER confirmation but BEFORE settlement
        val initialBalances = mapOf(
            genesis.node.getColdAddress() to genesis.node.getSelfBalance(),
            join1Address to join1.node.getSelfBalance(),
            join2.node.getColdAddress() to join2.node.getSelfBalance()
        )
        
        logSection("Waiting for reward settlement with confirmation weights")
        genesis.waitForStage(EpochStage.CLAIM_REWARDS, offset = 2)
        
        logSection("Verifying rewards are capped for Join1")
        val finalBalances = mapOf(
            genesis.node.getColdAddress() to genesis.node.getSelfBalance(),
            join1Address to join1.node.getSelfBalance(),
            join2.node.getColdAddress() to join2.node.getSelfBalance()
        )
        
        val genesisChange = finalBalances[genesis.node.getColdAddress()]!! - initialBalances[genesis.node.getColdAddress()]!!
        val join1Change = finalBalances[join1Address]!! - initialBalances[join1Address]!!
        val join2Change = finalBalances[join2.node.getColdAddress()]!! - initialBalances[join2.node.getColdAddress()]!!
        
        Logger.info("Balance changes:")
        Logger.info("  Genesis: $genesisChange (regular_weight=10, confirmation_weight=10)")
        Logger.info("  Join1: $join1Change (regular_weight=10, confirmation_weight=8)")
        Logger.info("  Join2: $join2Change (regular_weight=10, confirmation_weight=10)")
        
        // All participants should have positive rewards (Join1 not slashed, above alpha threshold)
        assertThat(genesisChange).isGreaterThan(0)
        assertThat(join1Change).isGreaterThan(0)
        assertThat(join2Change).isGreaterThan(0)
        Logger.info("  All participants received positive rewards")
        
        // Genesis and Join2 should have identical rewards (both full weight)
        logSection("Verifying Genesis and Join2 receive identical rewards")
        assertThat(genesisChange).isCloseTo(join2Change, Offset.offset(5L))
        Logger.info("  Genesis and Join2 received identical rewards: $genesisChange")
        
        // Join1 should have lower rewards due to capped confirmation weight (8 vs 10)
        // Expected ratio: join1Change / genesisChange ≈ 8/10 = 0.8
        logSection("Verifying Join1 rewards are capped proportionally")
        assertThat(join1Change).isLessThan(genesisChange)
        assertThat(join1Change).isLessThan(join2Change)
        Logger.info("  Join1 rewards are capped (lower than Genesis and Join2)")
        
        // Verify the ratio is approximately 8:10 (allowing some tolerance for rounding)
        val actualRatio = join1Change.toDouble() / genesisChange.toDouble()
        val expectedRatio = 8.0 / 10.0  // 0.8
        assertThat(actualRatio).isCloseTo(expectedRatio, Offset.offset(0.05))
        Logger.info("  Join1 reward ratio: $actualRatio (expected: $expectedRatio)")

        logSection("TEST PASSED: Confirmation PoC correctly caps rewards for lower confirmed weight")
    }
    
    // Helper functions
    
    private fun createConfirmationPoCSpec(
        expectedConfirmationsPerEpoch: Long
    ): Spec<AppState> {
        // Configure epoch params and confirmation PoC params
        // epochLength=40 provides sufficient inference phase window for confirmation PoC trigger
        // pocStageDuration=5, pocValidationDuration=4 gives confirmation PoC enough time to complete
        return spec {
            this[AppState::inference] = spec<InferenceState> {
                this[InferenceState::params] = spec<InferenceParams> {
                    this[InferenceParams::epochParams] = spec<EpochParams> {
                        this[EpochParams::epochLength] = 40L
                        this[EpochParams::pocStageDuration] = 5L
                        this[EpochParams::pocValidationDuration] = 4L
                        this[EpochParams::pocExchangeDuration] = 2L
                    }
                    this[InferenceParams::confirmationPocParams] = spec<ConfirmationPoCParams> {
                        this[ConfirmationPoCParams::expectedConfirmationsPerEpoch] = expectedConfirmationsPerEpoch
                        this[ConfirmationPoCParams::alphaThreshold] = Decimal.fromDouble(0.70)
                        this[ConfirmationPoCParams::slashFraction] = Decimal.fromDouble(0.10)
                    }
                    this[InferenceParams::pocParams] = spec<PocParams> {
                        this[PocParams::pocDataPruningEpochThreshold] = 10L
                    }
                }
            }
        }
    }
    
    private fun waitForConfirmationPoCTrigger(pair: LocalInferencePair, maxBlocks: Int = 100): ConfirmationPoCEvent? {
        var attempts = 0
        while (attempts < maxBlocks) {
            val epochData = pair.getEpochData()
            if (epochData.isConfirmationPocActive && epochData.activeConfirmationPocEvent != null) {
                return epochData.activeConfirmationPocEvent
            }
            pair.node.waitForNextBlock()
            attempts++
        }
        return null
    }
    
    private fun waitForConfirmationPoCPhase(
        pair: LocalInferencePair,
        targetPhase: ConfirmationPoCPhase,
        maxBlocks: Int = 100
    ) {
        var attempts = 0
        while (attempts < maxBlocks) {
            val epochData = pair.getEpochData()
            if (epochData.isConfirmationPocActive && 
                epochData.activeConfirmationPocEvent?.phase == targetPhase) {
                return
            }
            pair.node.waitForNextBlock()
            attempts++
        }
        error("Timeout waiting for confirmation PoC phase: $targetPhase")
    }
    
    private fun waitForConfirmationPoCCompletion(
        pair: LocalInferencePair,
        maxBlocks: Int = 100
    ) {
        var attempts = 0
        while (attempts < maxBlocks) {
            val epochData = pair.getEpochData()
            if (!epochData.isConfirmationPocActive) {
                return
            }
            pair.node.waitForNextBlock()
            attempts++
        }
        error("Timeout waiting for confirmation PoC completion")
    }
    
    private fun getConfirmationWeights(pair: LocalInferencePair): Map<String, Pair<Long, Long>> {
        // Query active participants to get both regular weight and confirmation_weight
        val activeParticipants = pair.api.getActiveParticipants()
        
        val weights = mutableMapOf<String, Pair<Long, Long>>()
        activeParticipants.activeParticipants.participants.forEach { participant ->
            // Regular weight is the sum of poc_weight across all ml_nodes
            val regularWeight = participant.mlNodes.flatMap { it.mlNodes }.sumOf { it.pocWeight }
            
            // For confirmation weight, we need to query the epoch group data
            // For now, we'll use the regular weight as a placeholder
            // In a real implementation, this would query the ValidationWeight.confirmation_weight field
            val confirmationWeight = regularWeight  // TODO: Query actual confirmation_weight from chain
            
            weights[participant.index] = Pair(regularWeight, confirmationWeight)
        }
        
        return weights
    }
}


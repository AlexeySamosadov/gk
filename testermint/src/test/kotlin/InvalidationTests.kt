import com.productscience.*
import com.productscience.data.InferencePayload
import com.productscience.data.InferenceStatus
import com.productscience.data.getParticipant
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.Timeout
import org.tinylog.kotlin.Logger
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.assertNotNull

class InvalidationTests : TestermintTest() {
    @Test
    @Timeout(15, unit = TimeUnit.MINUTES)
    @Order(Int.MAX_VALUE - 1)
    fun `test invalid gets removed and restored`() {
        val (cluster, genesis) = initCluster(mergeSpec = alwaysValidate)
        cluster.allPairs.forEach { pair ->
            pair.waitForMlNodesToLoad()
        }
        genesis.waitForNextInferenceWindow()

        val dispatcher = Executors.newFixedThreadPool(10).asCoroutineDispatcher()
        runBlocking(dispatcher) {
            val deferreds = (1..10).map {
                async {
                    InferenceTestHelper(cluster, genesis, responsePayload = "Invalid JSON!!").runFullInference()
                }
            }
            deferreds.awaitAll()
        }

        Logger.warn("Got invalid results, waiting for invalidation.")

        genesis.markNeedsReboot()
        logSection("Waiting for removal")
        genesis.node.waitForNextBlock(2)
        val participants = genesis.api.getActiveParticipants()
        val excluded = participants.excludedParticipants.firstOrNull()
        assertNotNull(excluded, "Participant was not excluded")
        assertThat(excluded.address).isEqualTo(genesis.node.getColdAddress())
        val genesisValidatorInfo = genesis.node.getValidatorInfo()
        val validators = genesis.node.getValidators()
        assertThat(validators.validators).hasSize(3)
        val genesisValidator = validators.validators.first { it.consensusPubkey.value == genesisValidatorInfo.key }
        assertThat(genesisValidator.tokens).isEqualTo(0)
        genesis.waitForNextEpoch()
        val newParticipants = genesis.api.getActiveParticipants()
        assertThat(newParticipants.excludedParticipants).isEmpty()
        val removedRestored = newParticipants.activeParticipants.getParticipant(genesis)
        assertNotNull(removedRestored, "Excluded participant was not restored")
    }

    // 6.5m
    @Test
    fun `test valid with invalid validator gets validated`() {
        val (cluster, genesis) = initCluster(mergeSpec = alwaysValidate)
        genesis.waitForNextInferenceWindow()
        cluster.allPairs.forEach { pair ->
            pair.waitForMlNodesToLoad()
        }
        val oddPair = cluster.joinPairs.last()
        oddPair.mock?.setInferenceResponse(defaultInferenceResponseObject.withMissingLogit())
        logSection("Getting invalid invalidation")
        val invalidResult =
            generateSequence { getInferenceResult(genesis) }
                .first { it.executorBefore.id != oddPair.node.getColdAddress() }
        // The oddPair will mark it as invalid and force a vote, which should fail (valid)

        Logger.warn("Got invalid result, waiting for validation.")
        logSection("Waiting for revalidation")
        genesis.node.waitForNextBlock(10)
        logSection("Verifying revalidation")
        val newState = genesis.api.getInference(invalidResult.inference.inferenceId)

        assertThat(newState.statusEnum).isEqualTo(InferenceStatus.VALIDATED)

    }

    @Test
    fun `test invalid gets marked invalid`() {
        var tries = 3
        val (cluster, genesis) = initCluster(reboot = true)
        genesis.waitForNextInferenceWindow(10)
        val oddPair = cluster.joinPairs.last()
        val badResponse = defaultInferenceResponseObject.withMissingLogit()
        oddPair.mock?.setInferenceResponse(badResponse)
        var newState: InferencePayload
        do {
            logSection("Trying to get invalid inference. Tries left: $tries")
            newState = getInferenceValidationState(genesis, oddPair)
        } while (newState.statusEnum != InferenceStatus.INVALIDATED && tries-- > 0)
        logSection("Verifying invalidation")
        assertThat(newState.statusEnum).isEqualTo(InferenceStatus.INVALIDATED)
    }

    @Test
    fun `full inference with invalid response payload`() {
        val (cluster, genesis) = initCluster(mergeSpec = alwaysValidate)
        cluster.allPairs.forEach { pair ->
            pair.waitForMlNodesToLoad()
        }

        val helper = InferenceTestHelper(cluster, genesis, responsePayload = "Invalid JSON!!")
        if (!genesis.getEpochData().safeForInference) {
            genesis.waitForStage(EpochStage.CLAIM_REWARDS, 3)
        }
        val inference = helper.runFullInference()
        // should be invalidated quickly
        genesis.node.waitForNextBlock(3)
        val inferencePayload = genesis.node.getInference(inference.inferenceId)
        assertNotNull(inferencePayload)
        assertThat(inferencePayload.inference.status).isEqualTo(InferenceStatus.INVALIDATED.value)
    }


}
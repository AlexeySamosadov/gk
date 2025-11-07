package com.productscience.mockserver.service

import io.ktor.client.*
import io.ktor.client.engine.cio.*
import io.ktor.client.request.*
import io.ktor.http.*
import kotlinx.coroutines.*
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import org.slf4j.LoggerFactory
import java.util.UUID

/**
 * Delivery modes for PoC batches.
 */
enum class DeliveryMode {
    WEBSOCKET,  // Only WebSocket (fail if not connected)
    HTTP,       // Only HTTP callbacks
    AUTO        // Try WebSocket first, fallback to HTTP (default)
}

/**
 * Service for handling webhook callbacks.
 */
class WebhookService(
    private val responseService: ResponseService,
    private val wsManager: WebSocketManager
) {
    private val client = HttpClient(CIO)
    private val mapper = jacksonObjectMapper()
    private val scope = CoroutineScope(Dispatchers.IO)
    private val logger = LoggerFactory.getLogger(WebhookService::class.java)

    // Default delay for validation webhooks (in milliseconds)
    private val validationWebhookDelay = 5000L

    // Default URL for batch validation webhooks
    private val batchValidationWebhookUrl = "http://localhost:9100/v1/poc-batches/validated"
    
    // Current delivery mode (default: AUTO)
    @Volatile
    var deliveryMode: DeliveryMode = DeliveryMode.AUTO

    /**
     * Extracts a value from a JSON string using a JSONPath-like expression.
     * This is a simplified version that only supports direct property access.
     */
    fun extractJsonValue(json: String, path: String): String? {
        if (path.startsWith("$.")) {
            val propertyName = path.substring(2)
            val jsonNode = mapper.readTree(json)
            return jsonNode.get(propertyName)?.asText()
        }
        return null
    }

    /**
     * Try to deliver a batch via WebSocket.
     * @param batchType "generated" or "validated"
     * @param batch The batch data as a map
     * @return true if delivered successfully via WebSocket, false otherwise
     */
    private fun tryDeliverViaWebSocket(batchType: String, batch: Map<String, Any?>): Boolean {
        if (!wsManager.isConnected()) {
            logger.debug("WebSocket not connected, cannot deliver batch")
            return false
        }
        
        val batchId = UUID.randomUUID().toString()
        
        // Queue the message
        if (!wsManager.queueBatchMessage(batchType, batch, batchId)) {
            logger.debug("Failed to queue batch message in WebSocket queue")
            return false
        }
        
        // Wait for acknowledgment with timeout (matching ML node sender.py)
        val ackReceived = wsManager.waitForAck(batchId, timeoutMs = 3000)
        
        if (ackReceived) {
            logger.info("Successfully delivered $batchType batch via WebSocket")
        } else {
            logger.warn("Timeout waiting for WebSocket ACK for $batchType batch")
        }
        
        return ackReceived
    }
    
    /**
     * Sends a webhook POST request after a delay.
     */
    fun sendDelayedWebhook(
        url: String,
        body: String,
        headers: Map<String, String> = mapOf("Content-Type" to "application/json"),
        delayMillis: Long = 1000
    ) {
        scope.launch {
            delay(delayMillis)
            try {
                client.post(url) {
                    headers {
                        headers.forEach { (key, value) ->
                            append(key, value)
                        }
                    }
                    contentType(ContentType.Application.Json)
                    setBody(body)
                }
                logger.info("Successfully delivered batch via HTTP callback to $url")
            } catch (e: Exception) {
                logger.error("Error sending webhook to $url: ${e.message}", e)
            }
        }
    }

    /**
     * Processes a webhook for the generate POC endpoint.
     */
    fun processGeneratePocWebhook(requestBody: String) {
        try {
            val jsonNode = mapper.readTree(requestBody)
            val url = jsonNode.get("url")?.asText()
            val publicKey = jsonNode.get("public_key")?.asText()
            val blockHash = jsonNode.get("block_hash")?.asText()
            val blockHeight = jsonNode.get("block_height")?.asInt()
            val nodeNumber = jsonNode.get("node_id")?.asInt() ?: 1

            logger.info("Processing generate POC webhook - URL: $url, PublicKey: $publicKey, BlockHeight: $blockHeight, NodeNumber: $nodeNumber")

            if (url != null && publicKey != null && blockHash != null && blockHeight != null) {
                val webhookUrl = "$url/generated"

                // Get the weight from the ResponseService, default to 10 if not set
                val weight = responseService.getPocResponseWeight() ?: 10L
                logger.info("Using weight for POC generation: $weight")

                // Use ResponseService to generate the webhook body
                val webhookBody = responseService.generatePocResponseBody(
                    weight,
                    publicKey,
                    blockHash,
                    blockHeight,
                    nodeNumber,
                )

                // Parse the webhook body to get batch data
                val batchData = mapper.readValue(webhookBody, Map::class.java) as Map<String, Any?>
                
                // Deliver based on mode
                when (deliveryMode) {
                    DeliveryMode.WEBSOCKET -> {
                        logger.info("Delivery mode: WEBSOCKET only")
                        if (!tryDeliverViaWebSocket("generated", batchData)) {
                            logger.error("Failed to deliver generated batch via WebSocket (WebSocket-only mode)")
                        }
                    }
                    DeliveryMode.HTTP -> {
                        logger.info("Delivery mode: HTTP only")
                        sendDelayedWebhook(webhookUrl, webhookBody)
                    }
                    DeliveryMode.AUTO -> {
                        logger.info("Delivery mode: AUTO (WebSocket with HTTP fallback)")
                        if (!tryDeliverViaWebSocket("generated", batchData)) {
                            logger.info("WebSocket delivery failed, falling back to HTTP")
                            sendDelayedWebhook(webhookUrl, webhookBody)
                        }
                    }
                }
            } else {
                logger.warn("Missing required fields in generate POC webhook request: url=$url, publicKey=$publicKey, blockHash=$blockHash, blockHeight=$blockHeight")
            }
        } catch (e: Exception) {
            logger.error("Error processing generate POC webhook: ${e.message}", e)
        }
    }

    /**
     * Processes a webhook for the validate POC batch endpoint.
     */
    fun processValidatePocBatchWebhook(requestBody: String) {
        try {
            val jsonNode = mapper.readTree(requestBody)
            val publicKey = jsonNode.get("public_key")?.asText()
            val blockHash = jsonNode.get("block_hash")?.asText()
            val blockHeight = jsonNode.get("block_height")?.asInt()
            val nonces = jsonNode.get("nonces")
            val dist = jsonNode.get("dist")

            logger.info("Processing validate POC batch webhook - PublicKey: $publicKey, BlockHeight: $blockHeight")

            if (publicKey != null && blockHash != null && blockHeight != null && nonces != null && dist != null) {
                // Create the webhook body using the values from the request
                val webhookBody = """
                    {
                      "public_key": "$publicKey",
                      "block_hash": "$blockHash",
                      "block_height": $blockHeight,
                      "nonces": $nonces,
                      "dist": $dist,
                      "received_dist": $dist,
                      "r_target": 0.5,
                      "fraud_threshold": 0.1,
                      "n_invalid": 0,
                      "probability_honest": 0.99,
                      "fraud_detected": false
                    }
                """.trimIndent()

                // Parse the webhook body to get batch data
                val batchData = mapper.readValue(webhookBody, Map::class.java) as Map<String, Any?>

                val keyName = (System.getenv("KEY_NAME") ?: "localhost")
                val webHookUrl = "http://$keyName-api:9100/v1/poc-batches/validated"
                
                // Deliver based on mode
                when (deliveryMode) {
                    DeliveryMode.WEBSOCKET -> {
                        logger.info("Delivery mode: WEBSOCKET only")
                        if (!tryDeliverViaWebSocket("validated", batchData)) {
                            logger.error("Failed to deliver validated batch via WebSocket (WebSocket-only mode)")
                        }
                    }
                    DeliveryMode.HTTP -> {
                        logger.info("Delivery mode: HTTP only")
                        logger.info("Sending batch validation webhook to $webHookUrl with delay: ${validationWebhookDelay}ms")
                        sendDelayedWebhook(webHookUrl, webhookBody, delayMillis = validationWebhookDelay)
                    }
                    DeliveryMode.AUTO -> {
                        logger.info("Delivery mode: AUTO (WebSocket with HTTP fallback)")
                        if (!tryDeliverViaWebSocket("validated", batchData)) {
                            logger.info("WebSocket delivery failed, falling back to HTTP")
                            logger.info("Sending batch validation webhook to $webHookUrl with delay: ${validationWebhookDelay}ms")
                            sendDelayedWebhook(webHookUrl, webhookBody, delayMillis = validationWebhookDelay)
                        }
                    }
                }
            } else {
                logger.warn("Missing required fields in validate POC batch webhook request: publicKey=$publicKey, blockHash=$blockHash, blockHeight=$blockHeight, nonces=$nonces, dist=$dist")
            }
        } catch (e: Exception) {
            logger.error("Error processing validate POC batch webhook: ${e.message}", e)
        }
    }
}

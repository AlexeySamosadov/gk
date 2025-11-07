package com.productscience.mockserver.routes

import io.ktor.server.application.*
import io.ktor.server.response.*
import io.ktor.server.routing.*
import io.ktor.server.request.*
import io.ktor.server.websocket.*
import io.ktor.websocket.*
import io.ktor.http.*
import com.productscience.mockserver.model.ModelState
import com.productscience.mockserver.model.PowState
import com.productscience.mockserver.service.WebhookService
import com.productscience.mockserver.service.WebSocketManager
import kotlinx.coroutines.*
import org.slf4j.LoggerFactory
import java.util.concurrent.TimeUnit

/**
 * Configures routes for POW-related endpoints.
 */
fun Route.powRoutes(webhookService: WebhookService, wsManager: WebSocketManager) {
    val logger = LoggerFactory.getLogger("PowRoutes")

    // POST /api/v1/pow/init/generate - Generates POC and transitions to POW state
    post("/api/v1/pow/init/generate") {
        handleInitGenerate(call, webhookService, logger)
    }

    // Versioned POST /{version}/api/v1/pow/init/generate - Generates POC and transitions to POW state
    post("/{version}/api/v1/pow/init/generate") {
        val version = call.parameters["version"]
        logger.info("Received versioned POW init/generate request for version: $version")
        handleInitGenerate(call, webhookService, logger)
    }

    // POST /api/v1/pow/init/validate - Validates POC
    post("/api/v1/pow/init/validate") {
        handleInitValidate(call, logger)
    }

    // Versioned POST /{version}/api/v1/pow/init/validate - Validates POC
    post("/{version}/api/v1/pow/init/validate") {
        val version = call.parameters["version"]
        logger.info("Received versioned POW init/validate request for version: $version")
        handleInitValidate(call, logger)
    }

    // POST /api/v1/pow/validate - Validates POC batch
    post("/api/v1/pow/validate") {
        handleValidateBatch(call, webhookService, logger)
    }

    // Versioned POST /{version}/api/v1/pow/validate - Validates POC batch
    post("/{version}/api/v1/pow/validate") {
        val version = call.parameters["version"]
        logger.info("Received versioned POW validate batch request for version: $version")
        handleValidateBatch(call, webhookService, logger)
    }

    get("/api/v1/pow/status") {
        handlePowStatus(call, logger)
    }

    // Versioned GET /{version}/api/v1/pow/status - Gets POW status
    get("/{version}/api/v1/pow/status") {
        val version = call.parameters["version"]
        logger.debug("Received versioned POW status request for version: $version")
        handlePowStatus(call, logger)
    }

    // WebSocket /api/v1/pow/ws - WebSocket endpoint for PoC batch delivery
    webSocket("/api/v1/pow/ws") {
        handleWebSocket(this, wsManager, logger)
    }
}

/**
 * Handles POW init/generate requests.
 */
private suspend fun handleInitGenerate(call: ApplicationCall, webhookService: WebhookService, logger: org.slf4j.Logger) {
    logger.info("Received POW init/generate request")
    
    // Update the state to POW
    if (ModelState.getCurrentState() != ModelState.STOPPED ||
        PowState.getCurrentState() != PowState.POW_STOPPED) {
        logger.warn("Invalid state for POW init/generate. Current state: ${ModelState.getCurrentState()}, POW state: ${PowState.getCurrentState()}")
        call.respond(HttpStatusCode.BadRequest, mapOf(
            "error" to "Invalid state for validation. state = ${ModelState.POW}. powState = ${PowState.POW_GENERATING}"
        ))
        return
    }

    ModelState.updateState(ModelState.POW)
    PowState.updateState(PowState.POW_GENERATING)
    logger.info("State updated to POW with POW_GENERATING substate")

    // Get the request body
    val requestBody = call.receiveText()
    logger.debug("Processing generate POC webhook with request body: $requestBody")

    // Process the webhook asynchronously
    webhookService.processGeneratePocWebhook(requestBody)

    // Respond with 200 OK
    call.respond(HttpStatusCode.OK)
}

/**
 * Handles POW init/validate requests.
 */
private suspend fun handleInitValidate(call: ApplicationCall, logger: org.slf4j.Logger) {
    logger.info("Received POW init/validate request")
    
    // This endpoint requires the state to be POW
    if (ModelState.getCurrentState() != ModelState.POW ||
        PowState.getCurrentState() != PowState.POW_GENERATING) {
        logger.warn("Invalid state for POW init/validate. Current state: ${ModelState.getCurrentState()}, POW state: ${PowState.getCurrentState()}")
        call.respond(HttpStatusCode.BadRequest, mapOf(
            "error" to "Invalid state for validation. state = ${ModelState.POW}. powState = ${PowState.POW_GENERATING}"
        ))
        return
    }

    PowState.updateState(PowState.POW_VALIDATING)
    logger.info("POW state updated to POW_VALIDATING")

    call.receiveText()

    // Respond with 200 OK
    call.respond(HttpStatusCode.OK)
}

/**
 * Handles POW validate batch requests.
 */
private suspend fun handleValidateBatch(call: ApplicationCall, webhookService: WebhookService, logger: org.slf4j.Logger) {
    logger.info("Received POW validate batch request")
    
    // This endpoint requires the state to be POW
    if (ModelState.getCurrentState() != ModelState.POW ||
        PowState.getCurrentState() != PowState.POW_VALIDATING) {
        logger.warn("Invalid state for POW validate batch. Current state: ${ModelState.getCurrentState()}, POW state: ${PowState.getCurrentState()}")
        call.respond(HttpStatusCode.BadRequest, mapOf("error" to "Invalid state for batch validation"))
        return
    }

    // Get the request body
    val requestBody = call.receiveText()
    logger.debug("Processing validate POC batch webhook with request body: $requestBody")

    // Process the webhook asynchronously
    webhookService.processValidatePocBatchWebhook(requestBody)

    // Respond with 200 OK
    call.respond(HttpStatusCode.OK)
}

/**
 * Handles POW status requests.
 */
private suspend fun handlePowStatus(call: ApplicationCall, logger: org.slf4j.Logger) {
    logger.debug("Received POW status request")
    // Respond with the current state
    call.respond(
        HttpStatusCode.OK,
        mapOf(
            "status" to PowState.getCurrentState(),
            "is_model_initialized" to false // FIXME: hardcoded for now, should be replaced with actual logic
        )
    )
}

/**
 * Handles WebSocket connections for PoC batch delivery.
 */
private suspend fun handleWebSocket(
    session: DefaultWebSocketServerSession,
    wsManager: WebSocketManager,
    logger: org.slf4j.Logger
) {
    // Check if PoW is running
    if (ModelState.getCurrentState() != ModelState.POW || 
        PowState.getCurrentState() == PowState.POW_STOPPED) {
        session.close(CloseReason(CloseReason.Codes.VIOLATED_POLICY, "PoW is not running"))
        logger.warn("WebSocket connection rejected: PoW is not running")
        return
    }
    
    // Try to register this connection
    if (!wsManager.registerConnection(session)) {
        session.close(CloseReason(CloseReason.Codes.VIOLATED_POLICY, "Another client is already connected"))
        logger.warn("WebSocket connection rejected: another client already connected")
        return
    }
    
    logger.info("WebSocket connection accepted")
    
    try {
        // Launch two coroutines: one for sending batches, one for receiving ACKs
        coroutineScope {
            val sendJob = launch {
                try {
                    sendBatches(session, wsManager, logger)
                } catch (e: Exception) {
                    logger.info("Send batches job terminated: ${e.message}")
                }
            }
            
            val receiveJob = launch {
                try {
                    receiveAcks(session, wsManager, logger)
                } catch (e: Exception) {
                    logger.info("Receive ACKs job terminated: ${e.message}")
                }
            }
            
            // Wait for either job to complete (usually due to error or disconnect)
            sendJob.join()
            receiveJob.join()
        }
    } finally {
        wsManager.unregisterConnection()
        logger.info("WebSocket connection closed")
    }
}

/**
 * Coroutine that sends batches from the queue to the WebSocket client.
 */
private suspend fun sendBatches(
    session: DefaultWebSocketServerSession,
    wsManager: WebSocketManager,
    logger: org.slf4j.Logger
) {
    try {
        while (true) {
            try {
                // Poll the queue with a timeout
                val message = withContext(Dispatchers.IO) {
                    wsManager.outQueue.poll(100, TimeUnit.MILLISECONDS)
                }
                
                if (message != null) {
                    // Send the message with a timeout
                    withTimeout(5000) {
                        session.send(Frame.Text(
                            """{"type":"${message.type}","batch":${formatBatch(message.batch)},"id":"${message.id}"}"""
                        ))
                        logger.debug("Sent ${message.type} batch to WebSocket client")
                    }
                } else {
                    // Small delay to avoid busy waiting
                    delay(100)
                }
            } catch (e: TimeoutCancellationException) {
                logger.error("Timeout sending batch to WebSocket client")
                throw e
            }
        }
    } catch (e: Exception) {
        logger.info("Send batches loop terminated: ${e.message}")
        throw e
    }
}

/**
 * Coroutine that receives acknowledgments from the WebSocket client.
 */
private suspend fun receiveAcks(
    session: DefaultWebSocketServerSession,
    wsManager: WebSocketManager,
    logger: org.slf4j.Logger
) {
    try {
        for (frame in session.incoming) {
            when (frame) {
                is Frame.Text -> {
                    val text = frame.readText()
                    try {
                        // Simple JSON parsing for ACK messages
                        if (text.contains("\"type\":\"ack\"")) {
                            val idMatch = Regex(""""id":"([^"]+)"""").find(text)
                            if (idMatch != null) {
                                val ackId = idMatch.groupValues[1]
                                wsManager.queueAck(ackId)
                                logger.info("Received ACK for batch $ackId")
                            }
                        }
                    } catch (e: Exception) {
                        logger.error("Error parsing ACK message: ${e.message}", e)
                    }
                }
                is Frame.Close -> {
                    logger.info("WebSocket close frame received")
                    return
                }
                else -> {
                    // Ignore other frame types
                }
            }
        }
    } catch (e: Exception) {
        logger.error("Error receiving ACK via WebSocket: ${e.message}", e)
        throw e
    }
}

/**
 * Helper function to format batch data as JSON string.
 */
private fun formatBatch(batch: Map<String, Any?>): String {
    val entries = batch.entries.joinToString(",") { (key, value) ->
        val valueStr = when (value) {
            is String -> "\"$value\""
            is Number -> value.toString()
            is Boolean -> value.toString()
            is List<*> -> value.joinToString(",", "[", "]") { it.toString() }
            null -> "null"
            else -> "\"$value\""
        }
        "\"$key\":$valueStr"
    }
    return "{$entries}"
}

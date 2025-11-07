package com.productscience.mockserver.routes

import io.ktor.server.application.*
import io.ktor.server.response.*
import io.ktor.server.routing.*
import io.ktor.server.request.*
import io.ktor.http.*
import com.productscience.mockserver.service.WebhookService
import com.productscience.mockserver.service.DeliveryMode
import org.slf4j.LoggerFactory

/**
 * Configures routes for configuration endpoints.
 */
fun Route.configRoutes(webhookService: WebhookService) {
    val logger = LoggerFactory.getLogger("ConfigRoutes")

    // POST /config/poc-delivery-mode - Set PoC batch delivery mode
    post("/config/poc-delivery-mode") {
        handleSetDeliveryMode(call, webhookService, logger)
    }

    // GET /config/poc-delivery-mode - Get current PoC batch delivery mode
    get("/config/poc-delivery-mode") {
        handleGetDeliveryMode(call, webhookService, logger)
    }
}

/**
 * Handles setting the PoC batch delivery mode.
 */
private suspend fun handleSetDeliveryMode(
    call: ApplicationCall,
    webhookService: WebhookService,
    logger: org.slf4j.Logger
) {
    try {
        val requestBody = call.receiveText()
        logger.info("Received set delivery mode request: $requestBody")

        // Parse the mode from request
        val modeMatch = Regex(""""mode"\s*:\s*"([^"]+)"""").find(requestBody)
        if (modeMatch == null) {
            call.respond(HttpStatusCode.BadRequest, mapOf("error" to "Missing 'mode' field"))
            return
        }

        val modeStr = modeMatch.groupValues[1].uppercase()
        val mode = try {
            DeliveryMode.valueOf(modeStr)
        } catch (e: IllegalArgumentException) {
            call.respond(
                HttpStatusCode.BadRequest,
                mapOf(
                    "error" to "Invalid mode: $modeStr. Valid modes: WEBSOCKET, HTTP, AUTO"
                )
            )
            return
        }

        webhookService.deliveryMode = mode
        logger.info("PoC batch delivery mode set to: $mode")

        call.respond(
            HttpStatusCode.OK,
            mapOf(
                "status" to "OK",
                "mode" to mode.toString()
            )
        )
    } catch (e: Exception) {
        logger.error("Error setting delivery mode: ${e.message}", e)
        call.respond(
            HttpStatusCode.InternalServerError,
            mapOf("error" to "Failed to set delivery mode: ${e.message}")
        )
    }
}

/**
 * Handles getting the current PoC batch delivery mode.
 */
private suspend fun handleGetDeliveryMode(
    call: ApplicationCall,
    webhookService: WebhookService,
    logger: org.slf4j.Logger
) {
    logger.debug("Received get delivery mode request")
    call.respond(
        HttpStatusCode.OK,
        mapOf(
            "mode" to webhookService.deliveryMode.toString()
        )
    )
}

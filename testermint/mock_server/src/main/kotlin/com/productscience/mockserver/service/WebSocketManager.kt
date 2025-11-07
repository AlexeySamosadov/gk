package com.productscience.mockserver.service

import io.ktor.server.websocket.*
import io.ktor.websocket.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.slf4j.LoggerFactory
import java.util.concurrent.BlockingQueue
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Data class representing a WebSocket message to send to the API node.
 */
data class WebSocketMessage(
    val type: String,        // "generated" or "validated"
    val batch: Map<String, Any?>,
    val id: String
)

/**
 * Data class representing an acknowledgment message from the API node.
 */
data class AckMessage(
    val type: String,        // "ack"
    val id: String,
    val timestamp: Long = System.currentTimeMillis()
)

/**
 * Manages WebSocket connections for PoC batch delivery.
 * Mimics the ML node side: accepts connections, sends batches, receives ACKs.
 */
class WebSocketManager {
    private val logger = LoggerFactory.getLogger(WebSocketManager::class.java)
    
    // Connection state
    private val connected = AtomicBoolean(false)
    private val connectionLock = Mutex()
    
    // Current WebSocket session (only one connection allowed)
    private var currentSession: WebSocketServerSession? = null
    
    // Queues for message passing
    val outQueue: BlockingQueue<WebSocketMessage> = LinkedBlockingQueue(100)
    val ackQueue: BlockingQueue<AckMessage> = LinkedBlockingQueue(100)
    
    /**
     * Check if a WebSocket client is currently connected.
     */
    fun isConnected(): Boolean = connected.get()
    
    /**
     * Attempt to register a new WebSocket connection.
     * Returns true if registration successful, false if another client is already connected.
     */
    suspend fun registerConnection(session: WebSocketServerSession): Boolean {
        return connectionLock.withLock {
            if (connected.get()) {
                logger.warn("WebSocket connection rejected: another client already connected")
                return false
            }
            currentSession = session
            connected.set(true)
            logger.info("WebSocket connection registered")
            true
        }
    }
    
    /**
     * Unregister the current WebSocket connection.
     */
    suspend fun unregisterConnection() {
        connectionLock.withLock {
            currentSession = null
            connected.set(false)
            // Clear queues to avoid stale messages
            outQueue.clear()
            ackQueue.clear()
            logger.info("WebSocket connection unregistered")
        }
    }
    
    /**
     * Send a batch message via WebSocket if connected.
     * @param batchType "generated" or "validated"
     * @param batch The batch data
     * @param batchId Unique identifier for this batch
     * @return true if message was queued successfully, false otherwise
     */
    fun queueBatchMessage(batchType: String, batch: Map<String, Any?>, batchId: String): Boolean {
        if (!connected.get()) {
            logger.debug("Cannot queue batch: WebSocket not connected")
            return false
        }
        
        val message = WebSocketMessage(
            type = batchType,
            batch = batch,
            id = batchId
        )
        
        return try {
            outQueue.offer(message)
        } catch (e: Exception) {
            logger.error("Failed to queue batch message: ${e.message}", e)
            false
        }
    }
    
    /**
     * Wait for an acknowledgment with the specified ID.
     * @param batchId The batch ID to wait for
     * @param timeoutMs Timeout in milliseconds
     * @return true if ACK received, false if timeout or error
     */
    fun waitForAck(batchId: String, timeoutMs: Long = 3000): Boolean {
        val startTime = System.currentTimeMillis()
        val collectedAcks = mutableListOf<AckMessage>()
        val maxAckAge = timeoutMs * 2
        
        while (System.currentTimeMillis() - startTime < timeoutMs) {
            try {
                val ack = ackQueue.poll(100, java.util.concurrent.TimeUnit.MILLISECONDS)
                if (ack != null) {
                    if (ack.id == batchId) {
                        logger.info("Received ACK for batch $batchId via WebSocket")
                        // Return stale ACKs to queue
                        collectedAcks.forEach { staleAck ->
                            ackQueue.offer(staleAck)
                        }
                        return true
                    } else {
                        // Check if ACK is not too old
                        val ackAge = System.currentTimeMillis() - ack.timestamp
                        if (ackAge < maxAckAge) {
                            collectedAcks.add(ack)
                        } else {
                            logger.debug("Discarding stale ACK ${ack.id} (age: ${ackAge}ms)")
                        }
                    }
                }
            } catch (e: InterruptedException) {
                logger.debug("Interrupted while waiting for ACK")
                return false
            }
        }
        
        logger.warn("Timeout waiting for ACK for batch $batchId")
        return false
    }
    
    /**
     * Queue an acknowledgment message (received from client).
     */
    fun queueAck(ackId: String) {
        val ack = AckMessage(
            type = "ack",
            id = ackId,
            timestamp = System.currentTimeMillis()
        )
        ackQueue.offer(ack)
    }
}


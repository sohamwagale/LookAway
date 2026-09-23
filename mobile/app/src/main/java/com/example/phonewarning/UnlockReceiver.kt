package com.example.phonewarning

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.URL
import kotlin.concurrent.thread

class UnlockReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        Log.d("PhoneWarning", "Broadcast received: ${intent.action}")

        if (intent.action == Intent.ACTION_SCREEN_ON) {
            Log.d("PhoneWarning", "💡 SCREEN ON")
        }

        if (intent.action == Intent.ACTION_USER_PRESENT) {
            Log.d("PhoneWarning", "📱 PHONE UNLOCKED! Sending signal to laptop...")

            val prefs = context.getSharedPreferences("PhoneWarningPrefs", Context.MODE_PRIVATE)
            val serverAddress = prefs.getString("server_address", "") ?: ""

            if (serverAddress.isBlank()) {
                Log.w("PhoneWarning", "⚠️ No laptop server address configured. Open the app to set IP.")
                return
            }

            sendUnlockNotification(serverAddress)
        }
    }

    private fun sendUnlockNotification(address: String) {
        thread {
            try {
                val formattedAddress = if (address.startsWith("http://") || address.startsWith("https://")) {
                    address
                } else {
                    "http://$address"
                }

                val fullUrl = if (formattedAddress.endsWith("/")) {
                    "${formattedAddress}phone/unlocked"
                } else {
                    "$formattedAddress/phone/unlocked"
                }

                Log.d("PhoneWarning", "Connecting to $fullUrl...")
                val url = URL(fullUrl)
                val conn = url.openConnection() as HttpURLConnection
                conn.requestMethod = "GET"
                conn.connectTimeout = 3000
                conn.readTimeout = 3000

                val responseCode = conn.responseCode
                if (responseCode == HttpURLConnection.HTTP_OK) {
                    val reader = BufferedReader(InputStreamReader(conn.inputStream))
                    val response = reader.readText()
                    reader.close()
                    Log.d("PhoneWarning", "✅ Successfully notified laptop! Server response: ${response.trim()}")
                } else {
                    Log.e("PhoneWarning", "❌ Server returned error code: $responseCode")
                }
                conn.disconnect()
            } catch (e: Exception) {
                Log.e("PhoneWarning", "❌ Failed to connect to laptop server: ${e.message}")
            }
        }
    }
}
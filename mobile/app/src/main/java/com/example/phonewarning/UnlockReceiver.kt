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

        val prefs = context.getSharedPreferences("PhoneWarningPrefs", Context.MODE_PRIVATE)
        val serverAddress = prefs.getString("server_address", "") ?: ""

        if (serverAddress.isBlank()) {
            Log.w("PhoneWarning", "⚠️ No laptop server address configured.")
            return
        }

        when (intent.action) {
            Intent.ACTION_SCREEN_ON -> {
                Log.d("PhoneWarning", "💡 SCREEN ON")
            }
            Intent.ACTION_USER_PRESENT -> {
                Log.d("PhoneWarning", "📱 PHONE UNLOCKED! Starting alert on laptop...")
                sendNotification(serverAddress, "phone/unlocked")
            }
            Intent.ACTION_SCREEN_OFF -> {
                Log.d("PhoneWarning", "🔒 PHONE LOCKED! Stopping alert on laptop...")
                sendNotification(serverAddress, "phone/locked")
            }
        }
    }

    private fun sendNotification(address: String, endpoint: String) {
        thread {
            try {
                val formattedAddress = if (address.startsWith("http://") || address.startsWith("https://")) {
                    address
                } else {
                    "http://$address"
                }

                val trimmedAddress = if (formattedAddress.endsWith("/")) {
                    formattedAddress
                } else {
                    "$formattedAddress/"
                }

                val fullUrl = "$trimmedAddress$endpoint"

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
                    Log.d("PhoneWarning", "✅ Success ($endpoint): ${response.trim()}")
                } else {
                    Log.e("PhoneWarning", "❌ Server returned code: $responseCode")
                }
                conn.disconnect()
            } catch (e: Exception) {
                Log.e("PhoneWarning", "❌ Connection failed ($endpoint): ${e.message}")
            }
        }
    }
}
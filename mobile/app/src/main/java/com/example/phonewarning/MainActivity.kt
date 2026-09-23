package com.example.phonewarning

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.util.Log
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.URL
import kotlin.concurrent.thread

class MainActivity : Activity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        Log.d("PhoneWarning", "MainActivity started — launching WatcherService")
        val serviceIntent = Intent(this, WatcherService::class.java)
        startForegroundService(serviceIntent)

        val prefs = getSharedPreferences("PhoneWarningPrefs", Context.MODE_PRIVATE)
        val etServerAddress = findViewById<EditText>(R.id.etServerAddress)
        val btnSave = findViewById<Button>(R.id.btnSave)
        val btnTest = findViewById<Button>(R.id.btnTest)
        val tvStatus = findViewById<TextView>(R.id.tvStatus)

        // Load saved server address
        val savedAddress = prefs.getString("server_address", "") ?: ""
        etServerAddress.setText(savedAddress)

        btnSave.setOnClickListener {
            val address = etServerAddress.text.toString().trim()
            prefs.edit().putString("server_address", address).apply()
            Toast.makeText(this, "Saved: $address", Toast.LENGTH_SHORT).show()
            tvStatus.text = "Saved address: $address"
        }

        btnTest.setOnClickListener {
            val address = etServerAddress.text.toString().trim()
            if (address.isBlank()) {
                tvStatus.text = "Please enter an IP:Port first"
                return@setOnClickListener
            }

            tvStatus.text = "Testing connection to $address..."
            thread {
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

                try {
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
                        runOnUiThread {
                            tvStatus.text = "✅ Success! Server responded: ${response.trim()}"
                        }
                    } else {
                        runOnUiThread {
                            tvStatus.text = "❌ HTTP Error: $responseCode"
                        }
                    }
                    conn.disconnect()
                } catch (e: Exception) {
                    runOnUiThread {
                        tvStatus.text = "❌ Connection failed: ${e.localizedMessage}"
                    }
                }
            }
        }
    }
}
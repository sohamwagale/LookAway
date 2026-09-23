package com.example.phonewarning

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.content.IntentFilter
import android.os.IBinder
import android.util.Log

class WatcherService : Service() {

    private val receiver = UnlockReceiver()
    private val CHANNEL_ID = "phonewarning_channel"
    private val NOTIF_ID = 1

    override fun onCreate() {
        super.onCreate()
        Log.d("PhoneWarning", "WatcherService started")

        createNotificationChannel()
        startForeground(NOTIF_ID, buildNotification())

        // Dynamic registration — works on API 26+ unlike manifest receivers
        val filter = IntentFilter().apply {
            addAction(Intent.ACTION_SCREEN_ON)
            addAction(Intent.ACTION_SCREEN_OFF)
            addAction(Intent.ACTION_USER_PRESENT)
        }
        registerReceiver(receiver, filter)
        Log.d("PhoneWarning", "Receiver registered dynamically")
    }

    override fun onDestroy() {
        super.onDestroy()
        unregisterReceiver(receiver)
        Log.d("PhoneWarning", "WatcherService destroyed, receiver unregistered")
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID,
            "PhoneWarning Monitor",
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = "Monitoring phone unlock events"
        }
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(channel)
    }

    private fun buildNotification(): Notification {
        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("PhoneWarning Active")
            .setContentText("Monitoring unlock events...")
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .build()
    }
}

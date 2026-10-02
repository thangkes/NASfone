package com.nasfone.client

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import com.nasfone.core.mobileclient.KeySigner
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.spec.ECGenParameterSpec

/**
 * The device key for one paired server. It is generated inside the Android
 * Keystore (StrongBox when the phone has one) and can only be used, never
 * read: the Go core receives signatures, not the key.
 */
class KeystoreKey(private val alias: String) : KeySigner {
    companion object {
        private const val STORE = "AndroidKeyStore"

        fun create(alias: String): KeystoreKey {
            fun spec(strongBox: Boolean) = KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setDigests(KeyProperties.DIGEST_NONE, KeyProperties.DIGEST_SHA256)
                .setIsStrongBoxBacked(strongBox)
                .build()
            val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, STORE)
            try {
                gen.initialize(spec(true))
                gen.generateKeyPair()
            } catch (_: Exception) {
                // No StrongBox (or it refuses EC without digest): the TEE keystore is fine.
                delete(alias)
                gen.initialize(spec(false))
                gen.generateKeyPair()
            }
            return KeystoreKey(alias)
        }

        fun delete(alias: String) {
            try {
                KeyStore.getInstance(STORE).apply { load(null) }.deleteEntry(alias)
            } catch (_: Exception) {
            }
        }
    }

    private fun store() = KeyStore.getInstance(STORE).apply { load(null) }

    override fun publicKeyDER(): ByteArray =
        store().getCertificate(alias)?.publicKey?.encoded ?: throw IllegalStateException("key $alias is missing")

    override fun signDigest(digest: ByteArray): ByteArray {
        val key = store().getKey(alias, null) as? PrivateKey ?: throw IllegalStateException("key $alias is missing")
        return Signature.getInstance("NONEwithECDSA").run {
            initSign(key)
            update(digest)
            sign() // ASN.1 DER, as the server expects
        }
    }
}

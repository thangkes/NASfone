import Foundation
import Security
import Nasfonecore

/// The device key for one paired server, created in the Secure Enclave
/// (falls back to the keychain where there is none, e.g. the simulator).
/// It can sign but never be read: the Go core only receives signatures.
final class EnclaveKey: NSObject, MobileclientKeySignerProtocol {
    let tag: String

    init(tag: String) { self.tag = tag }

    enum KeyError: Error { case missing, failed(String) }

    static func create(tag: String) throws -> EnclaveKey {
        func attrs(enclave: Bool) -> [String: Any] {
            var priv: [String: Any] = [
                kSecAttrIsPermanent as String: true,
                kSecAttrApplicationTag as String: Data(tag.utf8),
            ]
            // Usable after the first unlock, so photo backup can sign in the background.
            if let ac = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
                                                        enclave ? .privateKeyUsage : [], nil) {
                priv[kSecAttrAccessControl as String] = ac
            }
            var a: [String: Any] = [
                kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
                kSecAttrKeySizeInBits as String: 256,
                kSecPrivateKeyAttrs as String: priv,
            ]
            if enclave { a[kSecAttrTokenID as String] = kSecAttrTokenIDSecureEnclave }
            return a
        }
        var err: Unmanaged<CFError>?
        if SecKeyCreateRandomKey(attrs(enclave: true) as CFDictionary, &err) == nil {
            delete(tag: tag)
            err = nil
            guard SecKeyCreateRandomKey(attrs(enclave: false) as CFDictionary, &err) != nil else {
                throw KeyError.failed(err?.takeRetainedValue().localizedDescription ?? "key")
            }
        }
        return EnclaveKey(tag: tag)
    }

    static func delete(tag: String) {
        let q: [String: Any] = [
            kSecClass as String: kSecClassKey,
            kSecAttrApplicationTag as String: Data(tag.utf8),
        ]
        SecItemDelete(q as CFDictionary)
    }

    private func privateKey() throws -> SecKey {
        let q: [String: Any] = [
            kSecClass as String: kSecClassKey,
            kSecAttrApplicationTag as String: Data(tag.utf8),
            kSecAttrKeyType as String: kSecAttrKeyTypeECSECPrimeRandom,
            kSecReturnRef as String: true,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &item) == errSecSuccess, let k = item else { throw KeyError.missing }
        return k as! SecKey
    }

    /// DER SubjectPublicKeyInfo of the P-256 public key, as the server expects.
    func publicKeyDER() throws -> Data {
        guard let pub = SecKeyCopyPublicKey(try privateKey()) else { throw KeyError.missing }
        var err: Unmanaged<CFError>?
        guard let raw = SecKeyCopyExternalRepresentation(pub, &err) as Data? else {
            throw KeyError.failed(err?.takeRetainedValue().localizedDescription ?? "public key")
        }
        // X9.63 uncompressed point (65 bytes) wrapped in the fixed P-256 SPKI header.
        let header: [UInt8] = [0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01,
                               0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00]
        return Data(header) + raw
    }

    /// Signs a SHA-256 digest; returns an ASN.1 DER ECDSA signature.
    func signDigest(_ digest: Data?) throws -> Data {
        guard let digest = digest else { throw KeyError.failed("no digest") }
        var err: Unmanaged<CFError>?
        guard let sig = SecKeyCreateSignature(try privateKey(), .ecdsaSignatureDigestX962SHA256, digest as CFData, &err) as Data? else {
            throw KeyError.failed(err?.takeRetainedValue().localizedDescription ?? "sign")
        }
        return sig
    }
}

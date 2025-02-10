use anchor_lang::solana_program::secp256k1_recover::Secp256k1RecoverError;
use anchor_lang::solana_program::keccak::hash as keccak256;

pub fn ecrecover(hash: &[u8; 32], r: &[u8; 32], s: &[u8; 32], v: u8) -> Result<[u8;20], Secp256k1RecoverError> {
    let mut signature = [0u8; 64];
    signature[..32].copy_from_slice(r);
    signature[32..].copy_from_slice(s);

    // Convert v to recovery_id
    let recovery_id = if v >= 27 { v - 27 } else { v };

    let secp256k1_pubkey = anchor_lang::solana_program::secp256k1_recover::secp256k1_recover(hash, recovery_id, &signature)?;

    // Convert Secp256k1Pubkey to Ethereum address
    let pubkey_hash = keccak256(&secp256k1_pubkey.0[0..]);
    let eth_address: [u8; 20] = pubkey_hash.to_bytes()[12..32].try_into().unwrap();
    
    Ok(eth_address)
}

pub fn is_zero_address(address: &[u8; 20]) -> bool {
    address.iter().all(|&b| b == 0)
}
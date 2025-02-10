use hex_literal::hex;
use crate::evm::{ecrecover, is_zero_address};

#[test]
pub fn test_ecrecover() {
    let hash: [u8; 32]  = hex!("0449ed3a38f4f112c941f6bd456c128492f50c3c76be1822389afed9ea5e0b5d");
    let r: [u8; 32] = hex!("8c75b34415f03167d2c0dc6b7541c70943bc15eb5b69ce0daa4a6bcd52ce87a5");
    let s: [u8; 32] = hex!("36e4960d5792282d0e0df140c75ae225bf24e45b02fd7570a65a80bd2757f563");
    let v = 1;

    let derived_pubkey = ecrecover(&hash, &r, &s, v).unwrap();

    let expected_pubkey: [u8; 20] = hex!("3AcEb84A335051009bAAA1dC1F1B0FC65cafFC5e");

    assert_eq!(derived_pubkey, expected_pubkey);
}

#[test]
pub fn test_is_zero_address() {
    let address: [u8; 20] = [0u8; 20];
    assert!(is_zero_address(&address));
}
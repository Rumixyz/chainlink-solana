use std::time::Instant;
use crate::evm::encoding::Encoder;
use ethabi::{ParamType, Token};
use hex_literal::hex;
use hex::encode as hex_encode;
use crate::domain::SignedReport;

#[test]
fn test_encode_don_config_id() {
    let f: u8 = 1;

    let signer1: [u8; 20] = hex!("B12515f16bC0ff8952D786183B71f34466962808");
    let signer2: [u8; 20] = hex!("C9469292C39c3d16e3b2dA245d76d90615010b97");
    let signer3: [u8; 20] = hex!("13b0240f9F201076Fcd5AC5755D7D9cFd6d8Aea6");
    let signer4: [u8; 20] = hex!("46457CaBE2248EFF5BBa0F43888D558e288c1C58");

    let data = vec![signer1, signer2, signer3, signer4];
    let encoded = Encoder::encode_don_config_id(&data, f);
    let hex_encoded = hex_encode(&encoded);

    let expected_encoded = "000000000000000000000000b12515f16bc0ff8952d786183b71f34466962808000000000000000000000000c9469292c39c3d16e3b2da245d76d90615010b9700000000000000000000000013b0240f9f201076fcd5ac5755d7d9cfd6d8aea600000000000000000000000046457cabe2248eff5bba0f43888d558e288c1c5801";

    assert_eq!(hex_encoded, expected_encoded);
}


#[test]
fn test_decode_complex_with_raw_hex() {
    // Define the parameter types, including various types and nested structures
    let param_types = vec![
        ParamType::Uint(256),
        ParamType::Bool,
        ParamType::Address,
        ParamType::FixedBytes(32),
        ParamType::String,
        ParamType::Tuple(vec![ParamType::Uint(256), ParamType::Uint(256)]),
        ParamType::Array(ParamType::Uint(256).into()),
        ParamType::Tuple(vec![
            ParamType::Uint(256),
            ParamType::Tuple(vec![ParamType::Uint(256), ParamType::Uint(256)]),
        ]),
    ];

    // Raw hex data corresponding to the above parameter types
    let data = hex!(
        // Uint256: 1
        "0000000000000000000000000000000000000000000000000000000000000001"
        // Bool: true (1)
        "0000000000000000000000000000000000000000000000000000000000000001"
        // Address: 0x0000000000000000000000000000000000000002
        "0000000000000000000000000000000000000000000000000000000000000002"
        // FixedBytes32: 0x03 followed by zeros
        "0300000000000000000000000000000000000000000000000000000000000000"
        // Offset to the dynamic String parameter (352 bytes from start)
        "0000000000000000000000000000000000000000000000000000000000000160"
        // Tuple (Uint256, Uint256)
        // Uint256: 4
        "0000000000000000000000000000000000000000000000000000000000000004"
        // Uint256: 5
        "0000000000000000000000000000000000000000000000000000000000000005"
        // Offset to the dynamic Array parameter (416 bytes from start)
        "00000000000000000000000000000000000000000000000000000000000001a0"
        // Nested Tuple (Uint256, Tuple(Uint256, Uint256))
        // Uint256: 9
        "0000000000000000000000000000000000000000000000000000000000000009"
        // Inner Tuple Uint256: 10
        "000000000000000000000000000000000000000000000000000000000000000a"
        // Inner Tuple Uint256: 11
        "000000000000000000000000000000000000000000000000000000000000000b"
        // --- Dynamic parameters ---
        // String parameter at offset 352 bytes (0x160)
        // String length: 5
        "0000000000000000000000000000000000000000000000000000000000000005"
        // String data: "hello" (ASCII hex), padded to 32 bytes
        "68656c6c6f000000000000000000000000000000000000000000000000000000"
        // Array parameter at offset 416 bytes (0x1A0)
        // Array length: 3
        "0000000000000000000000000000000000000000000000000000000000000003"
        // Array elements: Uint256 values 6, 7, 8
        "0000000000000000000000000000000000000000000000000000000000000006"
        "0000000000000000000000000000000000000000000000000000000000000007"
        "0000000000000000000000000000000000000000000000000000000000000008"
    );

    // Create tokens with corresponding values
    let tokens = vec![
        Token::Uint(1.into()),
        Token::Bool(true),
        Token::Address("0000000000000000000000000000000000000002".parse().unwrap()),
        Token::FixedBytes({
            let mut bytes = vec![3u8];
            bytes.extend(vec![0u8; 31]);
            bytes
        }),
        Token::String("hello".into()),
        Token::Tuple(vec![Token::Uint(4.into()), Token::Uint(5.into())]),
        Token::Array(vec![
            Token::Uint(6.into()),
            Token::Uint(7.into()),
            Token::Uint(8.into()),
        ]),
        Token::Tuple(vec![
            Token::Uint(9.into()),
            Token::Tuple(vec![Token::Uint(10.into()), Token::Uint(11.into())]),
        ]),
    ];

    // Decode the binary data back into tokens
    let decoded = Encoder::decode(&param_types, &data).expect("Decoding failed");

    // Assert that the decoded tokens match the original tokens
    assert_eq!(decoded, tokens);
}

#[test]
fn test_decode_v3_report() {
    // Define the parameter types according to the schema
    let param_types = vec![
        // bytes32[3] reportContext
        ParamType::FixedArray(ParamType::FixedBytes(32).into(), 3),
        // bytes reportData
        ParamType::Bytes,
        // bytes32[] rs
        ParamType::Array(ParamType::FixedBytes(32).into()),
        // bytes32[] ss
        ParamType::Array(ParamType::FixedBytes(32).into()),
        // bytes32 rawVs
        ParamType::FixedBytes(32),
    ];

    // The hex data to decode
    let data = hex!(
        "0006bd87830d5f336e205cf5c63329a1dab8f5d56812eaeb7c69300e66ab8e22"
        "0000000000000000000000000000000000000000000000000000000017568517"
        "0000000000000000000000000000000000000000000000000000000000000000"
        "00000000000000000000000000000000000000000000000000000000000000e0"
        "0000000000000000000000000000000000000000000000000000000000000220"
        "0000000000000000000000000000000000000000000000000000000000000300"
        "0101000001010000000000000000000000000000000000000000000000000000"
        "0000000000000000000000000000000000000000000000000000000000000120"
        "00030ab7d02fbba9c6304f98824524407b1f494741174320cfd17a2c22eec1de"
        "0000000000000000000000000000000000000000000000000000000066f70fd5"
        "0000000000000000000000000000000000000000000000000000000066f70fd5"
        "00000000000000000000000000000000000000000000000000006be8db1c6cec"
        "0000000000000000000000000000000000000000000000000059a6ac2ae01b28"
        "0000000000000000000000000000000000000000000000000000000066f86155"
        "0000000000000000000000000000000000000000000000000918993b2e27d1c4"
        "00000000000000000000000000000000000000000000000009181122c12b3f3e"
        "00000000000000000000000000000000000000000000000009193714dabc0a3c"
        "0000000000000000000000000000000000000000000000000000000000000006"
        "8c75b34415f03167d2c0dc6b7541c70943bc15eb5b69ce0daa4a6bcd52ce87a5"
        "a6919524b2c7e5d09c5008ec768f9dc61dc67dce99d2bd1a2c64ee72e8e4e951"
        "4fbe5ff8ac79dff8d19815cc5eb610c5bc6a2493ae45da97250f504680e18955"
        "21b788d8593c637d9284d367901b5e1c22121f0c9184bd5c9fa7cabf3fe62acc"
        "2490ae1bf9fe3b006d8db1be172f94c82164004e0cff6fa4f1eb969d6c69c0e3"
        "b0d034a3c9dae9bc5f308474412b086d51f2be2dc8ff41908b5196eee1a81043"
        "0000000000000000000000000000000000000000000000000000000000000006"
        "36e4960d5792282d0e0df140c75ae225bf24e45b02fd7570a65a80bd2757f563"
        "07e48c64d284775de3f4ef733a57db0abc9848f794f25fc4031feca4bc0dda56"
        "136cbe31a94b8be0e836cef1b7c788214222a8daeffba13d3b96444eb0591824"
        "237f4304930d904c91cffe5063eba240974d0609ea4b7d39c7b239d96e4ae0e4"
        "6dbb79fd05dcfcf6bd1c6226ecdcf8166f74277abbfc036cd68e49e8c11f879b"
        "29100ca344bf0ccf431080cd93f8a39abba5cc326490201a11d2887fe4a3229f");

    // Decode the data
    let decoded_tokens = Encoder::decode(&param_types, &data).expect("Decoding failed");

    // Build expected tokens using slices from the data
    let expected_tokens = vec![
        // reportContext
        Token::FixedArray(vec![
            Token::FixedBytes(data[0..32].to_vec()),
            Token::FixedBytes(data[32..64].to_vec()),
            Token::FixedBytes(data[64..96].to_vec()),
        ]),
        // reportData (we can reuse the decoded token since it's dynamic)
        decoded_tokens[1].clone(),
        // rs (reuse decoded token)
        decoded_tokens[2].clone(),
        // ss (reuse decoded token)
        decoded_tokens[3].clone(),
        // rawVs
        Token::FixedBytes(data[192..224].to_vec()),
    ];

    // Assert that the decoded tokens match the expected tokens
    assert_eq!(decoded_tokens, expected_tokens);

    // Expected RS values from the report as Token::FixedBytes
    let expected_rs = Token::Array(vec![
        Token::FixedBytes(hex!("8c75b34415f03167d2c0dc6b7541c70943bc15eb5b69ce0daa4a6bcd52ce87a5").to_vec()),
        Token::FixedBytes(hex!("a6919524b2c7e5d09c5008ec768f9dc61dc67dce99d2bd1a2c64ee72e8e4e951").to_vec()),
        Token::FixedBytes(hex!("4fbe5ff8ac79dff8d19815cc5eb610c5bc6a2493ae45da97250f504680e18955").to_vec()),
        Token::FixedBytes(hex!("21b788d8593c637d9284d367901b5e1c22121f0c9184bd5c9fa7cabf3fe62acc").to_vec()),
        Token::FixedBytes(hex!("2490ae1bf9fe3b006d8db1be172f94c82164004e0cff6fa4f1eb969d6c69c0e3").to_vec()),
        Token::FixedBytes(hex!("b0d034a3c9dae9bc5f308474412b086d51f2be2dc8ff41908b5196eee1a81043").to_vec()),
    ]);

    // Compare expected_rs with the rs array from decoded_tokens
    assert_eq!(decoded_tokens[2], expected_rs);

    // Expected SS values from the report as Token::FixedBytes
    let expected_ss = Token::Array(vec![
        Token::FixedBytes(hex!("36e4960d5792282d0e0df140c75ae225bf24e45b02fd7570a65a80bd2757f563").to_vec()),
        Token::FixedBytes(hex!("07e48c64d284775de3f4ef733a57db0abc9848f794f25fc4031feca4bc0dda56").to_vec()),
        Token::FixedBytes(hex!("136cbe31a94b8be0e836cef1b7c788214222a8daeffba13d3b96444eb0591824").to_vec()),
        Token::FixedBytes(hex!("237f4304930d904c91cffe5063eba240974d0609ea4b7d39c7b239d96e4ae0e4").to_vec()),
        Token::FixedBytes(hex!("6dbb79fd05dcfcf6bd1c6226ecdcf8166f74277abbfc036cd68e49e8c11f879b").to_vec()),
        Token::FixedBytes(hex!("29100ca344bf0ccf431080cd93f8a39abba5cc326490201a11d2887fe4a3229f").to_vec()),
    ]);

    // Compare expected_ss with the ss array from decoded_tokens
    assert_eq!(decoded_tokens[3], expected_ss);


    let report_param_types = vec![
        // bytes32 feedId
        ParamType::FixedBytes(32),
        // uint32 lowerTimestamp
        ParamType::Uint(32),
        // uint32 observationsTimestamp
        ParamType::Uint(32),
        // uint192 nativeFee
        ParamType::Uint(192),
        // uint192 linkFee
        ParamType::Uint(192),
        // uint64 upperTimestamp
        ParamType::Uint(64),
        // int192 benchmark
        ParamType::Int(192),
        // int192 bid
        ParamType::Int(192),
        // int192 ask
        ParamType::Int(192),
    ];

    // Extract the bytes data from the Token::Bytes
    let report_data_bytes = match &decoded_tokens[1] {
        Token::Bytes(bytes) => bytes.as_slice(),
        _ => panic!("Expected Token::Bytes for reportData"),
    };

    // Decode the reportData
    let decoded_report_tokens = Encoder::decode(&report_param_types, report_data_bytes)
        .expect("Decoding reportData failed");

    // The expected report tokens
    let expected_report_tokens = vec![
        // feedId (bytes32)
        Token::FixedBytes(hex!("00030ab7d02fbba9c6304f98824524407b1f494741174320cfd17a2c22eec1de").to_vec()),
        // lowerTimestamp (uint32)
        Token::Uint(1727467477.into()),
        // observationsTimestamp (uint32)
        Token::Uint(1727467477.into()),
        // nativeFee (uint192)
        Token::Uint(ethabi::ethereum_types::U256::from_dec_str("118647852657900").unwrap()),
        // linkFee (uint192)
        Token::Uint(ethabi::ethereum_types::U256::from_dec_str("25234531311164200").unwrap()),
        // upperTimestamp (uint64)
        Token::Uint(1727553877.into()),
        // benchmark (int192)
        Token::Int(ethabi::ethereum_types::U256::from_dec_str("655442225238888900").unwrap()),
        // bid (int192)
        Token::Int(ethabi::ethereum_types::U256::from_dec_str("655292586749804350").unwrap()),
        // ask (int192)
        Token::Int(ethabi::ethereum_types::U256::from_dec_str("655615783467747900").unwrap()),
    ];

    // Assert that the decoded report tokens match the expected report tokens
    assert_eq!(decoded_report_tokens, expected_report_tokens);


    // Assert that the decoded report tokens match the expected report tokens
    assert_eq!(decoded_report_tokens, expected_report_tokens);
}

#[test]
fn test_parse_report() {
    let test_report_input = hex!("0006bd87830d5f336e205cf5c63329a1dab8f5d56812eaeb7c69300e66ab8e220000000000000000000000000000000000000000000000000000000017568517000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000e0000000000000000000000000000000000000000000000000000000000000022000000000000000000000000000000000000000000000000000000000000003000101000001010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000012000030ab7d02fbba9c6304f98824524407b1f494741174320cfd17a2c22eec1de0000000000000000000000000000000000000000000000000000000066f70fd50000000000000000000000000000000000000000000000000000000066f70fd500000000000000000000000000000000000000000000000000006be8db1c6cec0000000000000000000000000000000000000000000000000059a6ac2ae01b280000000000000000000000000000000000000000000000000000000066f861550000000000000000000000000000000000000000000000000918993b2e27d1c400000000000000000000000000000000000000000000000009181122c12b3f3e00000000000000000000000000000000000000000000000009193714dabc0a3c00000000000000000000000000000000000000000000000000000000000000068c75b34415f03167d2c0dc6b7541c70943bc15eb5b69ce0daa4a6bcd52ce87a5a6919524b2c7e5d09c5008ec768f9dc61dc67dce99d2bd1a2c64ee72e8e4e9514fbe5ff8ac79dff8d19815cc5eb610c5bc6a2493ae45da97250f504680e1895521b788d8593c637d9284d367901b5e1c22121f0c9184bd5c9fa7cabf3fe62acc2490ae1bf9fe3b006d8db1be172f94c82164004e0cff6fa4f1eb969d6c69c0e3b0d034a3c9dae9bc5f308474412b086d51f2be2dc8ff41908b5196eee1a81043000000000000000000000000000000000000000000000000000000000000000636e4960d5792282d0e0df140c75ae225bf24e45b02fd7570a65a80bd2757f56307e48c64d284775de3f4ef733a57db0abc9848f794f25fc4031feca4bc0dda56136cbe31a94b8be0e836cef1b7c788214222a8daeffba13d3b96444eb0591824237f4304930d904c91cffe5063eba240974d0609ea4b7d39c7b239d96e4ae0e46dbb79fd05dcfcf6bd1c6226ecdcf8166f74277abbfc036cd68e49e8c11f879b29100ca344bf0ccf431080cd93f8a39abba5cc326490201a11d2887fe4a3229f");

    let start = Instant::now();
    let signed_report = Encoder::parse_signed_report(&test_report_input);
    let duration = start.elapsed();
    println!("Parse time taken: {} microseconds", duration.as_micros());

    let parsed_report = signed_report.unwrap();

    // Define the expected SignedReport based on the input
    let expected_parsed_report = SignedReport {
        report_context: &[
            hex!("0006bd87830d5f336e205cf5c63329a1dab8f5d56812eaeb7c69300e66ab8e22"),
            hex!("0000000000000000000000000000000000000000000000000000000017568517"),
            hex!("0000000000000000000000000000000000000000000000000000000000000000")
        ],
        report_data: &Vec::from(hex!("00030ab7d02fbba9c6304f98824524407b1f494741174320cfd17a2c22eec1de0000000000000000000000000000000000000000000000000000000066f70fd50000000000000000000000000000000000000000000000000000000066f70fd500000000000000000000000000000000000000000000000000006be8db1c6cec0000000000000000000000000000000000000000000000000059a6ac2ae01b280000000000000000000000000000000000000000000000000000000066f861550000000000000000000000000000000000000000000000000918993b2e27d1c400000000000000000000000000000000000000000000000009181122c12b3f3e00000000000000000000000000000000000000000000000009193714dabc0a3c")),
        rs: &vec![
            hex!("8c75b34415f03167d2c0dc6b7541c70943bc15eb5b69ce0daa4a6bcd52ce87a5"),
            hex!("a6919524b2c7e5d09c5008ec768f9dc61dc67dce99d2bd1a2c64ee72e8e4e951"),
            hex!("4fbe5ff8ac79dff8d19815cc5eb610c5bc6a2493ae45da97250f504680e18955"),
            hex!("21b788d8593c637d9284d367901b5e1c22121f0c9184bd5c9fa7cabf3fe62acc"),
            hex!("2490ae1bf9fe3b006d8db1be172f94c82164004e0cff6fa4f1eb969d6c69c0e3"),
            hex!("b0d034a3c9dae9bc5f308474412b086d51f2be2dc8ff41908b5196eee1a81043")
        ],
        ss: &vec![
            hex!("36e4960d5792282d0e0df140c75ae225bf24e45b02fd7570a65a80bd2757f563"),
            hex!("07e48c64d284775de3f4ef733a57db0abc9848f794f25fc4031feca4bc0dda56"),
            hex!("136cbe31a94b8be0e836cef1b7c788214222a8daeffba13d3b96444eb0591824"),
            hex!("237f4304930d904c91cffe5063eba240974d0609ea4b7d39c7b239d96e4ae0e4"),
            hex!("6dbb79fd05dcfcf6bd1c6226ecdcf8166f74277abbfc036cd68e49e8c11f879b"),
            hex!("29100ca344bf0ccf431080cd93f8a39abba5cc326490201a11d2887fe4a3229f")
        ],
        raw_vs: &hex!("0101000001010000000000000000000000000000000000000000000000000000"),
    };

    assert_eq!(parsed_report, expected_parsed_report);
}

#[test]
fn test_parse_timestamp_from_report() {
    let test_report_data = hex!("00030ab7d02fbba9c6304f98824524407b1f494741174320cfd17a2c22eec1de0000000000000000000000000000000000000000000000000000000066f70fd50000000000000000000000000000000000000000000000000000000066f70fd500000000000000000000000000000000000000000000000000006be8db1c6cec0000000000000000000000000000000000000000000000000059a6ac2ae01b280000000000000000000000000000000000000000000000000000000066f861550000000000000000000000000000000000000000000000000918993b2e27d1c400000000000000000000000000000000000000000000000009181122c12b3f3e00000000000000000000000000000000000000000000000009193714dabc0a3c");
    let report = Encoder::parse_report_details_from_report(&test_report_data).unwrap();
    assert_eq!(*report.feed_id, hex!("00030ab7d02fbba9c6304f98824524407b1f494741174320cfd17a2c22eec1de"));
    assert_eq!(report.report_timestamp, 1727467477);
}

#[test]
fn test_decode_report_time() {
    // Define the parameter types according to the schema
    let param_types = vec![
        // bytes32[3] reportContext
        ParamType::FixedArray(ParamType::FixedBytes(32).into(), 3),
        // bytes reportData
        ParamType::Bytes,
        // bytes32[] rs
        ParamType::Array(ParamType::FixedBytes(32).into()),
        // bytes32[] ss
        ParamType::Array(ParamType::FixedBytes(32).into()),
        // bytes32 rawVs
        ParamType::FixedBytes(32),
    ];

    let report_param_types = vec![
        // bytes32 feedId
        ParamType::FixedBytes(32),
        // uint32 lowerTimestamp
        ParamType::Uint(32),
        // uint32 observationsTimestamp
        ParamType::Uint(32),
        // uint192 nativeFee
        ParamType::Uint(192),
        // uint192 linkFee
        ParamType::Uint(192),
        // uint64 upperTimestamp
        ParamType::Uint(64),
        // int192 benchmark
        ParamType::Int(192),
        // int192 bid
        ParamType::Int(192),
        // int192 ask
        ParamType::Int(192),
    ];
    // The hex data to decode
    let data = hex!(
    "0006bd87830d5f336e205cf5c63329a1dab8f5d56812eaeb7c69300e66ab8e22"
    "0000000000000000000000000000000000000000000000000000000017568517"
    "0000000000000000000000000000000000000000000000000000000000000000"
    "00000000000000000000000000000000000000000000000000000000000000e0"
    "0000000000000000000000000000000000000000000000000000000000000220"
    "0000000000000000000000000000000000000000000000000000000000000300"
    "0101000001010000000000000000000000000000000000000000000000000000"
    "0000000000000000000000000000000000000000000000000000000000000120"
    "00030ab7d02fbba9c6304f98824524407b1f494741174320cfd17a2c22eec1de"
    "0000000000000000000000000000000000000000000000000000000066f70fd5"
    "0000000000000000000000000000000000000000000000000000000066f70fd5"
    "00000000000000000000000000000000000000000000000000006be8db1c6cec"
    "0000000000000000000000000000000000000000000000000059a6ac2ae01b28"
    "0000000000000000000000000000000000000000000000000000000066f86155"
    "0000000000000000000000000000000000000000000000000918993b2e27d1c4"
    "00000000000000000000000000000000000000000000000009181122c12b3f3e"
    "00000000000000000000000000000000000000000000000009193714dabc0a3c"
    "0000000000000000000000000000000000000000000000000000000000000006"
    "8c75b34415f03167d2c0dc6b7541c70943bc15eb5b69ce0daa4a6bcd52ce87a5"
    "a6919524b2c7e5d09c5008ec768f9dc61dc67dce99d2bd1a2c64ee72e8e4e951"
    "4fbe5ff8ac79dff8d19815cc5eb610c5bc6a2493ae45da97250f504680e18955"
    "21b788d8593c637d9284d367901b5e1c22121f0c9184bd5c9fa7cabf3fe62acc"
    "2490ae1bf9fe3b006d8db1be172f94c82164004e0cff6fa4f1eb969d6c69c0e3"
    "b0d034a3c9dae9bc5f308474412b086d51f2be2dc8ff41908b5196eee1a81043"
    "0000000000000000000000000000000000000000000000000000000000000006"
    "36e4960d5792282d0e0df140c75ae225bf24e45b02fd7570a65a80bd2757f563"
    "07e48c64d284775de3f4ef733a57db0abc9848f794f25fc4031feca4bc0dda56"
    "136cbe31a94b8be0e836cef1b7c788214222a8daeffba13d3b96444eb0591824"
    "237f4304930d904c91cffe5063eba240974d0609ea4b7d39c7b239d96e4ae0e4"
    "6dbb79fd05dcfcf6bd1c6226ecdcf8166f74277abbfc036cd68e49e8c11f879b"
    "29100ca344bf0ccf431080cd93f8a39abba5cc326490201a11d2887fe4a3229f");

    // Record current timestamp
    let current_timestamp = Instant::now();

    // Decode the data
    let decoded_tokens = Encoder::decode(&param_types, &data).expect("Decoding failed");

    // Extract the bytes data from the Token::Bytes
    let report_data_bytes = match &decoded_tokens[1] {
        Token::Bytes(bytes) => bytes.as_slice(),
        _ => panic!("Expected Token::Bytes for reportData"),
    };

    // Decode the reportData
    Encoder::decode(&report_param_types, report_data_bytes)
        .expect("Decoding reportData failed");

    // Print the time taken for decoding in microseconds
    println!("Time taken for decoding: {} microseconds", current_timestamp.elapsed().as_micros());
}

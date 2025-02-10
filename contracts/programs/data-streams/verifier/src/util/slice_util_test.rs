use crate::util::slice_util::SliceUtil;

#[test]
fn test_no_duplicates() {
    let no_duplicates = vec![
        [0x12; 20],
        [0x34; 20],
        [0x56; 20],
        [0x78; 20],
    ];
    assert!(!SliceUtil::has_duplicates(&no_duplicates), "Expected to not detect duplicates");
}

#[test]
fn test_has_duplicates() {
    let has_duplicates = vec![
        [0x12; 20],
        [0x34; 20],
        [0x56; 20],
        [0x12; 20], // Duplicate of the first element
    ];
    assert!(SliceUtil::has_duplicates(&has_duplicates), "Expected to detect duplicates");
}


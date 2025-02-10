#[derive(Debug, PartialEq)]
pub struct SignedReport<'a> {
    pub report_context:  &'a [[u8; 32]; 3],
    pub report_data: &'a [u8],
    pub rs: &'a [[u8; 32]],
    pub ss: &'a [[u8; 32]],
    pub raw_vs: &'a [u8; 32],
}

#[derive(Debug, PartialEq)]
pub struct Report<'a> {
    pub feed_id: &'a [u8; 32],
    pub report_timestamp: u32,
}

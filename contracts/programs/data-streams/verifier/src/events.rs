use anchor_lang::prelude::*;

#[event]
pub struct ConfigActivated {
    pub don_config_id: String,
    pub is_active: bool,
}

#[event]
pub struct ConfigRemoved {
    pub don_config_id: String,
}

#[event]
pub struct ReportVerified {
    pub feed_id: [u8; 32],
    pub requester: Pubkey,
}

#[event]
pub struct ConfigSet {
    pub don_config_id: String,
    pub signers: Vec<[u8; 20]>,
    pub f: u8,
    pub don_config_index: u16,
}
#[event]
pub struct AccessControllerSet {
    pub access_controller: Pubkey,
}
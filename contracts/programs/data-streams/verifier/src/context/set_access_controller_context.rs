use crate::state::VerifierAccount;
use crate::errors::ErrorCode;
use access_controller_data_streams::AccessController;
use anchor_lang::prelude::*;

#[derive(Accounts)]
pub struct SetAccessControllerContext<'info> {
    #[account(mut, seeds = [b"verifier"], bump)]
    pub verifier_account: AccountLoader<'info, VerifierAccount>,
    #[account(address = verifier_account.load()?.verifier_account_config.owner @ ErrorCode::Unauthorized)]
    pub owner: Signer<'info>,
    pub access_controller: Option<AccountLoader<'info, AccessController>>,
}


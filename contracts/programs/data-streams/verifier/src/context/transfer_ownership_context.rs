use crate::state::VerifierAccount;
use crate::errors::ErrorCode;
use anchor_lang::prelude::*;

#[derive(Accounts)]
pub struct TransferOwnershipContext<'info> {
    #[account(mut, seeds = [b"verifier"], bump)]
    pub verifier_account: AccountLoader<'info, VerifierAccount>,
    #[account(address = verifier_account.load()?.verifier_account_config.owner @ ErrorCode::Unauthorized)]
    pub owner: Signer<'info>,
}


use crate::state::VerifierAccount;
use anchor_lang::prelude::*;
use crate::errors::ErrorCode;

#[derive(Accounts)]
pub struct AcceptOwnershipContext<'info> {
    #[account(mut, seeds = [b"verifier"], bump)]
    pub verifier_account: AccountLoader<'info, VerifierAccount>,
    #[account(address = verifier_account.load()?.verifier_account_config.proposed_owner @ ErrorCode::Unauthorized)]
    pub owner: Signer<'info>,
}

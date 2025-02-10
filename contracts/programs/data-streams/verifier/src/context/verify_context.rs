use crate::errors::ErrorCode;
use crate::state::VerifierAccount;
use anchor_lang::prelude::*;

#[derive(Accounts)]
pub struct VerifyContext<'info> {
    #[account(seeds = [b"verifier"], bump)]
    pub verifier_account: AccountLoader<'info, VerifierAccount>,
    #[account(
        constraint = 
            verifier_account.load()?.verifier_account_config.access_controller == Pubkey::default() ||
            verifier_account.load()?.verifier_account_config.access_controller == access_controller.key() 
            @ ErrorCode::InvalidAccessController)]
    pub access_controller: AccountLoader<'info, access_controller_data_streams::AccessController>,
    #[account(
        constraint =
            verifier_account.load()?.verifier_account_config.access_controller == Pubkey::default() ||
            access_controller_data_streams::has_access(&access_controller, &user.key())?
            @ ErrorCode::Unauthorized
    )]
    pub user: Signer<'info>,
    /// CHECK: Program will validate this based on report input.
    pub config_account: UncheckedAccount<'info>
}

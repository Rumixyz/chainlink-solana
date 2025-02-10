use crate::state::{VerifierAccount};
use anchor_lang::prelude::*;
use crate::program::Verifier;
use crate::errors::ErrorCode;

#[derive(Accounts)]
pub struct InitializeAccountDataContext<'info> {
    #[account(mut, seeds = [b"verifier"], bump)]
    pub verifier_account: AccountLoader<'info, VerifierAccount>,
    pub owner: Signer<'info>,
    pub access_controller: Option<AccountLoader<'info, access_controller_data_streams::AccessController>>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()))]
    pub program: Program<'info, Verifier>,
    #[account(constraint = program_data.upgrade_authority_address == Some(owner.key()) @ ErrorCode::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    pub system_program: Program<'info, System>,
}
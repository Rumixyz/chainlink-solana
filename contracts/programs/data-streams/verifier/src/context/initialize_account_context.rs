use crate::state::VerifierAccount;
use anchor_lang::prelude::*;
use crate::program::Verifier;
use crate::errors::ErrorCode;

#[derive(Accounts)]
pub struct InitializeContext<'info> {
    #[account(
        init,
        seeds = [b"verifier"],
        bump,
        payer = owner,
        space = VerifierAccount::INIT_SPACE)]
    pub verifier_account: AccountLoader<'info, VerifierAccount>,
    #[account(mut)]
    pub owner: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()))]
    pub program: Program<'info, Verifier>,
    #[account(constraint = program_data.upgrade_authority_address == Some(owner.key()) @ ErrorCode::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    pub system_program: Program<'info, System>,
}
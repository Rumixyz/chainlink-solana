use crate::state::VerifierAccount;
use anchor_lang::prelude::*;
use anchor_lang::solana_program::system_program;
use crate::program::Verifier;
use crate::errors::ErrorCode;

#[derive(Accounts)]
#[instruction(len: u32)]
pub struct ReallocContext<'info> {
    #[account(
        mut,
        seeds = [b"verifier"],
        bump,
        realloc = len as usize,
        realloc::zero = true,
        realloc::payer=owner
    )]
    pub verifier_account: AccountLoader<'info, VerifierAccount>,
    #[account(mut)]
    pub owner: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()))]
    pub program: Program<'info, Verifier>,
    #[account(constraint = program_data.upgrade_authority_address == Some(owner.key()) @ ErrorCode::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(address = system_program::ID)]
    pub system_program: Program<'info, System>,
}

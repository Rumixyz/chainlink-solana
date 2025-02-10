use crate::state::VerifierAccount;
use anchor_lang::prelude::*;


#[derive(Accounts)]
pub struct ShrinkAccount<'info> {
    #[account(mut)]
    /// CHECK: do the thing
    pub large_account: AccountInfo<'info>,
    #[account(signer)]
    pub authority: Signer<'info>,
}

#[derive(Accounts)]
pub struct CloseLargeAccount<'info> {
    #[account(mut)]
    pub large_account: AccountInfo<'info>,
    #[account(mut)]
    pub recipient: Signer<'info>,
}

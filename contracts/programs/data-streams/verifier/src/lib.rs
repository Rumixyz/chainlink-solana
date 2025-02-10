//! SPDX-License-Identifier: MIT
pub mod common;
pub mod context;
pub mod domain;
pub mod errors;
pub mod events;
pub mod evm;
pub mod state;
pub mod util;

use anchor_lang::prelude::*;
use anchor_lang::solana_program::keccak::hash as keccak256;
use common::*;
use context::*;
use domain::*;
use events::*;
use evm::*;
use state::*;
use util::*;

use evm::Encoder;
use hex::encode as hex_encode;
use std::borrow::BorrowMut;
use std::cell::Ref;

declare_id!("Gt9S41PtjR58CbG9JhJ3J6vxesqrNAswbWYbLNTMZA3c");

#[program]
pub mod verifier {
    use super::*;
    use solana_program::program::{set_return_data};

    /// # Arguments
    ///
    /// * `signed_report` - The snappy compressed signed report data to be verified.
    ///
    /// # Returns
    ///
    /// A vector of bytes representing the verified report data.
    ///
    /// # Errors
    ///
    /// Returns an error if the verification fails due to invalid signatures, mismatched signers, or other validation issues.
    pub fn verify(ctx: Context<VerifyContext>, signed_report: Vec<u8>) -> Result<()> {
        let verifier_account = ctx.accounts.verifier_account.load()?;

        let report_data = verify_report(&ctx, &signed_report, &verifier_account)?;

        set_return_data(&report_data);

        Ok(())
    }

    pub fn set_config_with_activation_time(
        ctx: Context<UpdateConfigContext>,
        signers: Vec<[u8; 20]>,
        f: u8,
        activation_time: u32,
    ) -> Result<()> {
        require!(f > 0, errors::ErrorCode::FaultToleranceMustBePositive);

        require!(
            signers.len() > 3 * f as usize,
            errors::ErrorCode::InsufficientSigners
        );

        require!(
            signers.len() <= MAX_NUMBER_OF_ORACLES as usize,
            errors::ErrorCode::ExcessSigners
        );

        // Check that activationTime is not in the future.
        require!(
            activation_time <= Clock::get()?.unix_timestamp as u32,
            errors::ErrorCode::BadActivationTime
        );

        let mut verifier_account = ctx.accounts.verifier_account.load_mut()?;

        // Check we haven't reached the max number of configs
        require!(
            verifier_account.don_configs.len() < MAX_NUMBER_OF_DON_CONFIGS,
            errors::ErrorCode::MaxNumberOfConfigsReached
        );

        // Sort signers to ensure donConfigId is deterministic.
        let mut sorted_signers = signers;
        sorted_signers.sort_unstable();

        // Check for duplicate addresses in signers.
        require!(
            !SliceUtil::has_duplicates_sorted(&sorted_signers),
            errors::ErrorCode::NonUniqueSignatures
        );

        let don_config_id = Encoder::compute_don_config_id(&Encoder::encode_don_config_id(&sorted_signers, f));

        // Check if there are any existing configs
        if let Some(last_don_config) = verifier_account.don_configs.last() {
            // Check the config we're setting isn't already set as the current active config as this will increase search costs unnecessarily when verifying historic reports
            require!(
                last_don_config.don_config_id != don_config_id,
                errors::ErrorCode::DonConfigAlreadyExists
            );

            // Check that activation time is after the last config
            require!(
                last_don_config.activation_time < activation_time,
                errors::ErrorCode::BadActivationTime
            );
        }

        // Register the signers for this DON
        let mut signers_array = SigningKeys::default();
        for signer in sorted_signers.iter() {
            require!(
                !is_zero_address(signer),
                errors::ErrorCode::ZeroAddress
            );

            signers_array.push(SigningKey {
                key: *signer,
            });
        }

        verifier_account.don_configs.push(DonConfig {
            don_config_id,
            f,
            is_active: 1,
            activation_time,
            _padding: 0,
            signers: signers_array,
        });

        emit!(ConfigSet {
            don_config_id: hex_encode(don_config_id),
            signers: sorted_signers,
            f,
            don_config_index: (verifier_account.don_configs.len() - 1) as u16,
        });

        Ok(())
    }

    pub fn set_config(
        ctx: Context<UpdateConfigContext>,
        signers: Vec<[u8; 20]>,
        f: u8,
    ) -> Result<()> {
        set_config_with_activation_time(
            ctx,
            signers,
            f,
            Clock::get()?.unix_timestamp as u32,
        )
    }

    pub fn set_config_active(
        ctx: Context<UpdateConfigContext>,
        don_config_index: u64,
        is_active: u8,
    ) -> Result<()> {
        let mut verifier_account = ctx.accounts.verifier_account.load_mut()?;
        require!(
            don_config_index < verifier_account.don_configs.len() as u64,
            errors::ErrorCode::DonConfigDoesNotExist
        );
        verifier_account.don_configs[don_config_index as usize].is_active = is_active;
        emit!(ConfigActivated {
            don_config_id: hex_encode(
                verifier_account.don_configs[don_config_index as usize].don_config_id
            ),
            is_active: is_active != 0,
        });
        Ok(())
    }

    pub fn remove_latest_config(ctx: Context<UpdateConfigContext>) -> Result<()> {
        let mut verifier_account = ctx.accounts.verifier_account.load_mut()?;
        require!(
            !verifier_account.don_configs.is_empty(),
            errors::ErrorCode::DonConfigDoesNotExist
        );
        let c = verifier_account.don_configs.pop().unwrap();
        emit!(ConfigRemoved {
            don_config_id: hex_encode(c.don_config_id)
        });
        Ok(())
    }

    // Used to Set the access controller
    // We use an optional access controller. 
    // See https://github.com/coral-xyz/anchor/pull/2101 on how option works in Anchor 
    pub fn set_access_controller(ctx: Context<SetAccessControllerContext>) -> Result<()> {
        let mut verifier_account = ctx.accounts.verifier_account.load_mut()?;

        verifier_account.verifier_account_config.access_controller = ctx.accounts.access_controller
            .as_ref()
            .map_or(Pubkey::default(), |ac| ac.key());

        emit!(AccessControllerSet {
            access_controller: verifier_account.verifier_account_config.access_controller.key()
        });
        Ok(())
    }

    // initialize into existence the verifier account. You must realloc after this
    pub fn initialize(
        _ctx: Context<InitializeContext>
    ) -> Result<()> {
        // Will init PDA but does not attempt to load the account struct as PDA size is too small
        Ok(())
    }

    // Initializes the verifier (admin) account data. Call after initialize + realloc
    // We use an optional access controller. 
    // See https://github.com/coral-xyz/anchor/pull/2101 on how option works in Anchor 
    pub fn initialize_account_data(
        ctx: Context<InitializeAccountDataContext>
    ) -> Result<()> {
        let mut verifier_account = ctx.accounts.verifier_account.load_mut()?;
        require!(verifier_account.version == 0, errors::ErrorCode::InvalidInputs); // assert uninitialized state
        verifier_account.version = 1;
        if let Some(access_controller) = &ctx.accounts.access_controller {
            verifier_account.verifier_account_config.access_controller = access_controller.key();
        }
        verifier_account.verifier_account_config.owner = ctx.accounts.owner.key();
        Ok(())
    }

    pub fn realloc_account(_ctx: Context<ReallocContext>, _len: u32) -> Result<()> {
        msg!("Reallocated to len: {}", _len as usize);
        Ok(())
    }

    pub fn transfer_ownership(
        ctx: Context<TransferOwnershipContext>,
        proposed_owner: Pubkey,
    ) -> Result<()> {
        let mut verifier_account = ctx.accounts.verifier_account.load_mut()?;
        verifier_account.verifier_account_config.proposed_owner = proposed_owner;
        Ok(())
    }

    pub fn accept_ownership(ctx: Context<AcceptOwnershipContext>) -> Result<()> {
        let mut verifier_account = ctx.accounts.verifier_account.load_mut()?;
        verifier_account.verifier_account_config.owner = std::mem::take(&mut verifier_account.verifier_account_config.proposed_owner);
        Ok(())
    }
}

fn verify_report(
    ctx: &Context<VerifyContext>,
    signed_report: &[u8],
    verifier_account: &Ref<VerifierAccount>,
) -> Result<Vec<u8>> {
    let decompressed_report = Compressor::decompress(signed_report);

    let SignedReport {
        report_context,
        report_data,
        rs,
        ss,
        raw_vs,
    } = Encoder::parse_signed_report(&decompressed_report)?;

    let (expected_config_account, _) = Pubkey::find_program_address(&[&report_context[0]], &ID);
    require!(
        expected_config_account == ctx.accounts.config_account.key(),
        errors::ErrorCode::InvalidConfigAccount
    );

    // Validate signature lengths
    require!(
        rs.len() == ss.len(),
        errors::ErrorCode::MismatchedSignatures
    );

    // Ensure there is at least one signer
    require!(
        !rs.is_empty(),
        errors::ErrorCode::NoSigners
    );

    let signed_payload_hash = keccak256(&[
        &keccak256(report_data).to_bytes()[..],
        &report_context[0][..],
        &report_context[1][..],
        &report_context[2][..]
    ].concat());

    // Recover signer addresses from signatures
    let mut signers = Vec::with_capacity(rs.len());
    for i in 0..rs.len() {
        let addr = ecrecover(
            &signed_payload_hash.to_bytes(),
            &rs[i],
            &ss[i],
            raw_vs[i],
        ).map_err(|_| errors::ErrorCode::BadVerification)?;
        require!(
            !is_zero_address(&addr),
            errors::ErrorCode::BadVerification
        );
        signers.push(addr);
    }

    // Checking for duplicate signatures
    require!(
        !SliceUtil::has_duplicates(&signers),
        errors::ErrorCode::BadVerification
    );

    // Parse report details from report_data
    let report = Encoder::parse_report_details_from_report(report_data)?;

    // Find the active DON configuration based on the report timestamp
    let active_don_config = verifier_account
        .don_configs
        .iter()
        .rev()
        .find(|config| config.activation_time <= report.report_timestamp)
        .ok_or(errors::ErrorCode::BadVerification)?;

    // Ensure the active DON is indeed active
    require!(
        active_don_config.is_active != 0,
        errors::ErrorCode::ConfigDeactivated
    );

    // Verify that the number of signers exceeds the threshold 'f'
    require!(
        signers.len() > active_don_config.f as usize,
        errors::ErrorCode::BadVerification
    );

    // Get registered signers
    let registered_signers: Vec<&[u8; 20]> = active_don_config
        .signers
        .iter()
        .map(|s| &s.key)
        .collect();

    // Check each signer is registered
    for signer in signers.iter() {
        if !registered_signers.iter().any(|registered| signer == *registered) {
            return Err(errors::ErrorCode::BadVerification.into());
        }
    }

    emit!(ReportVerified {
        feed_id: *report.feed_id,
        requester: ctx.accounts.user.key(),
    });

    Ok(report_data.to_vec())
}
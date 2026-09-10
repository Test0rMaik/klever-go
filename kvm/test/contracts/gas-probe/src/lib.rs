#![no_std]

// Rebuild (no manifest, matching the rest of this corpus - no test contract here ships a Cargo.toml):
//
//   rustc --edition 2021 --target wasm32-unknown-unknown --crate-type cdylib \
//         -C opt-level=z -C lto -C panic=abort -o output/gas-probe.wasm src/lib.rs

//! Purpose-built probes for the managed-buffer append gas change.
//!
//! These call the VM hooks directly instead of going through klever-sc, so each endpoint's hook
//! call sequence is exactly what it looks like - no framework encoding in the middle. The shapes
//! mirror what the SDK generates for the patterns that matter: accumulating a result in a loop
//! (ManagedVec::push / ManagedBufferBuilder) and a read-modify-write of a struct held in a mapper.

#[panic_handler]
fn on_panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

#[allow(dead_code)]
extern "C" {
    fn mBufferNew() -> i32;
    fn mBufferSetBytes(handle: i32, offset: *const u8, length: i32) -> i32;
    fn mBufferAppendBytes(handle: i32, offset: *const u8, length: i32) -> i32;
    fn mBufferAppend(accumulator: i32, data: i32) -> i32;
    fn mBufferGetLength(handle: i32) -> i32;
    fn mBufferStorageStore(key: i32, source: i32) -> i32;
    fn mBufferStorageLoad(key: i32, destination: i32) -> i32;
    fn mBufferFinish(handle: i32) -> i32;
    fn smallIntGetUnsignedArgument(id: i32) -> i64;
}

static CHUNK: [u8; 64] = [0x41u8; 64];
static KEY: [u8; 8] = *b"probekey";

#[no_mangle]
pub extern "C" fn init() {}

#[no_mangle]
pub extern "C" fn upgrade() {}

/// accumulate(iterations, chunkLen): builds one buffer by appending chunkLen bytes per iteration
/// and returns it. This is the ManagedVec::push / ManagedBufferBuilder shape - the accumulator
/// grows on every call and nothing is written to storage in between.
#[no_mangle]
pub extern "C" fn accumulate() {
    unsafe {
        let iterations = smallIntGetUnsignedArgument(0) as i32;
        let chunk_len = clamp_chunk(smallIntGetUnsignedArgument(1) as i32);

        let accumulator = mBufferNew();
        for _ in 0..iterations {
            mBufferAppendBytes(accumulator, CHUNK.as_ptr(), chunk_len);
        }

        mBufferFinish(accumulator);
    }
}

/// accumulateViaHandles(iterations, chunkLen): same loop through mBufferAppend, so the other
/// append hook is exercised on an identical shape
#[no_mangle]
pub extern "C" fn accumulateViaHandles() {
    unsafe {
        let iterations = smallIntGetUnsignedArgument(0) as i32;
        let chunk_len = clamp_chunk(smallIntGetUnsignedArgument(1) as i32);

        let accumulator = mBufferNew();
        let chunk = mBufferNew();
        mBufferSetBytes(chunk, CHUNK.as_ptr(), chunk_len);

        for _ in 0..iterations {
            mBufferAppend(accumulator, chunk);
        }

        mBufferFinish(accumulator);
    }
}

/// readModifyWrite(iterations, structLen): the mapper + custom struct pattern. Each iteration
/// loads the stored value, rebuilds it field by field through appends, and writes it back - so
/// every append is followed by a storage write, which is where StorePerByte dwarfs DataCopyPerByte.
#[no_mangle]
pub extern "C" fn readModifyWrite() {
    unsafe {
        let iterations = smallIntGetUnsignedArgument(0) as i32;
        let struct_len = clamp_chunk(smallIntGetUnsignedArgument(1) as i32);
        // a struct encoded as 4 fields, the way nested-encode appends one field at a time
        let field_len = if struct_len / 4 > 0 { struct_len / 4 } else { 1 };

        let key = mBufferNew();
        mBufferSetBytes(key, KEY.as_ptr(), KEY.len() as i32);

        for _ in 0..iterations {
            let loaded = mBufferNew();
            mBufferStorageLoad(key, loaded);

            let encoded = mBufferNew();
            for _ in 0..4 {
                mBufferAppendBytes(encoded, CHUNK.as_ptr(), field_len);
            }

            mBufferStorageStore(key, encoded);
        }

        mBufferFinish(key);
    }
}

fn clamp_chunk(requested: i32) -> i32 {
    if requested <= 0 {
        1
    } else if requested > CHUNK.len() as i32 {
        CHUNK.len() as i32
    } else {
        requested
    }
}
